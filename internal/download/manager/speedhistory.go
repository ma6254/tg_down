package manager

import (
	"sort"
	"sync"
	"time"
)

// 历史速度采样参数。
const (
	// speedSampleInterval 是历史速度的采样间隔；与任务进度落库的节奏接近。
	speedSampleInterval = 1 * time.Second
	// speedHistoryRetention 是内存里保留的历史时长，超出后覆盖最旧样本。
	speedHistoryRetention = 24 * time.Hour
	// speedHistoryCapacity 是环形缓冲容量，按采样间隔保留完整窗口。
	speedHistoryCapacity = int(speedHistoryRetention / speedSampleInterval)
	// defaultSpeedHistoryPoints 是自动选择聚合粒度时的目标点数上限。
	defaultSpeedHistoryPoints = 300
	// maxSpeedHistoryPoints 是显式 bucket 时允许的最大点数，防止响应过大。
	maxSpeedHistoryPoints = 1000
	// minSpeedHistoryBucket / maxSpeedHistoryBucket 是允许的聚合粒度范围。
	minSpeedHistoryBucket = speedSampleInterval
	maxSpeedHistoryBucket = time.Hour
)

// speedBucketSteps 是自动粒度可选的「好看」步长，按从小到大排列。
var speedBucketSteps = []time.Duration{
	time.Second,
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
	15 * time.Second,
	30 * time.Second,
	time.Minute,
	2 * time.Minute,
	5 * time.Minute,
	10 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
	time.Hour,
}

// SpeedHistoryQuery 是历史速度的查询条件。
type SpeedHistoryQuery struct {
	From   time.Time
	To     time.Time
	Bucket time.Duration // 为 0 时自动选择
}

// SpeedHistoryPoint 是曲线上的一个聚合点。
type SpeedHistoryPoint struct {
	At time.Time
	// Speed 是桶内样本的平均速度，B/s。
	Speed int64
	// Running 是桶内最大运行任务数。
	Running int
}

// SpeedHistory 是一次历史速度查询的结果。
type SpeedHistory struct {
	From           time.Time
	To             time.Time
	Bucket         time.Duration
	SampleInterval time.Duration
	// StatsSince 是平均/峰值统计的起点：上次清空时间或请求窗口起点。
	StatsSince time.Time
	// AverageSpeed 是查询范围内全部原始样本的平均值，含空闲时的 0 值。
	AverageSpeed int64
	// PeakSpeed 是查询范围内单个原始样本的最大值。
	PeakSpeed int64
	Points    []SpeedHistoryPoint
}

// speedSample 是一次采样：某一时刻的合计速度与运行任务数。
type speedSample struct {
	at      time.Time
	speed   int64
	running int
}

// speedHistory 是固定容量的速度采样环形缓冲。
type speedHistory struct {
	mu      sync.Mutex
	samples []speedSample
	next    int
	size    int
}

// newSpeedHistory 创建容量为 capacity 的环形缓冲。
func newSpeedHistory(capacity int) *speedHistory {
	if capacity < 1 {
		capacity = 1
	}
	return &speedHistory{samples: make([]speedSample, capacity)}
}

// add 写入一条采样；缓冲满后覆盖最旧的样本。
func (h *speedHistory) add(sample speedSample) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.samples[h.next] = sample
	h.next = (h.next + 1) % len(h.samples)
	if h.size < len(h.samples) {
		h.size++
	}
}

// snapshot 按时间顺序返回样本副本。
func (h *speedHistory) snapshot() []speedSample {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.size == 0 {
		return nil
	}
	out := make([]speedSample, 0, h.size)
	start := h.next - h.size
	if start < 0 {
		start += len(h.samples)
	}
	for i := 0; i < h.size; i++ {
		out = append(out, h.samples[(start+i)%len(h.samples)])
	}
	return out
}

// sampleSpeedHistory 周期记录合计速度，直到服务停止。
func (m *Manager) sampleSpeedHistory() {
	defer m.wg.Done()

	ticker := time.NewTicker(speedSampleInterval)
	defer ticker.Stop()

	// 先记一条，刚启动的服务也能立刻查到空载曲线。
	m.recordSpeedSample(time.Now())
	for {
		select {
		case <-m.ctx.Done():
			return
		case at := <-ticker.C:
			m.recordSpeedSample(at)
		}
	}
}

// recordSpeedSample 读取一次实时总览并写入环形缓冲。
func (m *Manager) recordSpeedSample(at time.Time) {
	stats := m.Stats()
	m.speedHistory.add(speedSample{
		at:      at,
		speed:   stats.TotalSpeed,
		running: stats.Running,
	})
}

// ResetSpeedStats 清空平均/峰值统计的起点；速度曲线样本保留。
//
// 清空后 average_speed / peak_speed 只累计清空时间之后的采样，
// 已经画出来的曲线点不受影响。
func (m *Manager) ResetSpeedStats() time.Time {
	now := time.Now()
	m.resetSpeedStatsAt(now)
	return now
}

// resetSpeedStatsAt 指定统计起点；抽出来便于测试。
func (m *Manager) resetSpeedStatsAt(at time.Time) {
	m.speedStatsResetAt.Store(at.UnixNano())
}

// SpeedHistory 返回给定时间范围内的速度历史，按 bucket 聚合。
//
// 数据只来自内存；查询范围早于最旧样本时从最旧样本开始，晚于当前时间时
// 按当前时间处理。bucket 为 0 或越界时自动选择粒度。
func (m *Manager) SpeedHistory(query SpeedHistoryQuery) SpeedHistory {
	resetAt := time.Unix(0, m.speedStatsResetAt.Load())
	to := query.To
	if now := time.Now(); to.After(now) {
		to = now
	}
	from := query.From

	result := SpeedHistory{
		From:           from,
		To:             to,
		Bucket:         query.Bucket,
		SampleInterval: speedSampleInterval,
		StatsSince:     laterTime(from, resetAt),
		Points:         []SpeedHistoryPoint{},
	}
	if m.speedHistory == nil || !from.Before(to) {
		return result
	}

	samples := m.speedHistory.snapshot()
	if len(samples) == 0 {
		return result
	}
	// 查询范围完全落在样本之外。
	if samples[len(samples)-1].at.Before(from) || samples[0].at.After(to) {
		return result
	}
	if from.Before(samples[0].at) {
		from = samples[0].at
	}
	result.From = from
	summaryFrom := laterTime(from, resetAt)
	result.StatsSince = summaryFrom

	bucket := query.Bucket
	if bucket < minSpeedHistoryBucket || bucket > maxSpeedHistoryBucket {
		bucket = autoSpeedBucket(from, to)
	}
	// 显式粒度过细时兜底到自动粒度，避免单次响应点数失控。
	if int(to.Sub(from)/bucket)+1 > maxSpeedHistoryPoints {
		bucket = autoSpeedBucket(from, to)
	}
	result.Bucket = bucket

	bucketSeconds := int64(bucket / time.Second)
	if bucketSeconds <= 0 {
		bucketSeconds = 1
	}

	type bucketAcc struct {
		sum     int64
		count   int
		running int
	}
	buckets := make(map[int64]*bucketAcc)
	var total int64
	var count int
	var peak int64

	for _, sample := range samples {
		if sample.at.Before(from) || sample.at.After(to) {
			continue
		}

		// 曲线点覆盖整个请求窗口。
		at := sample.at.Unix()
		at -= at % bucketSeconds
		acc := buckets[at]
		if acc == nil {
			acc = &bucketAcc{}
			buckets[at] = acc
		}
		acc.sum += sample.speed
		acc.count++
		if sample.running > acc.running {
			acc.running = sample.running
		}

		// 平均/峰值只统计上次清空之后（或窗口起点之后）的样本。
		if sample.at.Before(summaryFrom) {
			continue
		}
		total += sample.speed
		count++
		if sample.speed > peak {
			peak = sample.speed
		}
	}

	keys := make([]int64, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	points := make([]SpeedHistoryPoint, 0, len(keys))
	for _, key := range keys {
		acc := buckets[key]
		points = append(points, SpeedHistoryPoint{
			At:      time.Unix(key, 0),
			Speed:   acc.sum / int64(acc.count),
			Running: acc.running,
		})
	}
	if count > 0 {
		result.AverageSpeed = total / int64(count)
		result.PeakSpeed = peak
	}
	result.Points = points
	return result
}

// laterTime 返回较晚的时间；用于把统计起点收敛到清空时间。
func laterTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// autoSpeedBucket 选择使点数不超过目标值的最小步长。
func autoSpeedBucket(from, to time.Time) time.Duration {
	span := to.Sub(from)
	for _, step := range speedBucketSteps {
		if int(span/step)+1 <= defaultSpeedHistoryPoints {
			return step
		}
	}
	return speedBucketSteps[len(speedBucketSteps)-1]
}
