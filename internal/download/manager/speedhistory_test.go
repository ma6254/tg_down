package manager

import (
	"context"
	"testing"
	"time"
)

// TestSpeedHistoryRingOrder 确认环形缓冲写满后按时间顺序覆盖最旧样本。
func TestSpeedHistoryRingOrder(t *testing.T) {
	history := newSpeedHistory(3)
	base := time.Unix(1763800000, 0)
	for i := 0; i < 5; i++ {
		history.add(speedSample{
			at:    base.Add(time.Duration(i) * time.Second),
			speed: int64(i),
		})
	}

	samples := history.snapshot()
	if len(samples) != 3 {
		t.Fatalf("样本数 = %d，期望 3", len(samples))
	}
	for i, want := range []int64{2, 3, 4} {
		if samples[i].speed != want {
			t.Errorf("第 %d 个样本速度 = %d，期望 %d", i, samples[i].speed, want)
		}
	}
	if empty := newSpeedHistory(1).snapshot(); len(empty) != 0 {
		t.Errorf("空缓冲应返回 0 个样本，实际 %d", len(empty))
	}
}

// TestSpeedHistoryAggregates 确认按 bucket 聚合的均值、最大值与区间汇总。
func TestSpeedHistoryAggregates(t *testing.T) {
	m := New(Config{Workers: 1})
	t.Cleanup(func() { m.Stop(context.Background()) })

	base := time.Unix(1763800000, 0)
	m.speedHistory.add(speedSample{at: base.Add(3 * time.Second), speed: 100, running: 1})
	m.speedHistory.add(speedSample{at: base.Add(4 * time.Second), speed: 300, running: 2})
	m.speedHistory.add(speedSample{at: base.Add(11 * time.Second), speed: 500, running: 1})

	history := m.SpeedHistory(SpeedHistoryQuery{
		From:   base,
		To:     base.Add(20 * time.Second),
		Bucket: 10 * time.Second,
	})
	if history.AverageSpeed != 300 || history.PeakSpeed != 500 {
		t.Errorf("区间汇总 = 均值 %d / 峰值 %d，期望 300/500",
			history.AverageSpeed, history.PeakSpeed)
	}
	if history.Bucket != 10*time.Second || history.SampleInterval != speedSampleInterval {
		t.Errorf("bucket = %v，采样间隔 = %v", history.Bucket, history.SampleInterval)
	}
	if len(history.Points) != 2 {
		t.Fatalf("聚合点数 = %d，期望 2：%+v", len(history.Points), history.Points)
	}
	first := history.Points[0]
	if first.At.Unix() != base.Unix() || first.Speed != 200 || first.Running != 2 {
		t.Errorf("第一个桶 = %+v，期望 at=%d speed=200 running=2", first, base.Unix())
	}
	second := history.Points[1]
	if second.At.Unix() != base.Add(10*time.Second).Unix() || second.Speed != 500 {
		t.Errorf("第二个桶 = %+v，期望 at=%d speed=500",
			second, base.Add(10*time.Second).Unix())
	}
}

// TestSpeedHistoryRange 确认范围收敛与区间外查询。
func TestSpeedHistoryRange(t *testing.T) {
	m := New(Config{Workers: 1})
	t.Cleanup(func() { m.Stop(context.Background()) })

	base := time.Unix(1763800000, 0)
	m.speedHistory.add(speedSample{at: base.Add(3 * time.Second), speed: 100})

	// from 早于最旧样本：收敛到最旧样本，不再返回更早的空区间。
	history := m.SpeedHistory(SpeedHistoryQuery{
		From: base.Add(-time.Hour),
		To:   base.Add(10 * time.Second),
	})
	if !history.From.Equal(base.Add(3 * time.Second)) {
		t.Errorf("From = %v，期望收敛到 %v", history.From, base.Add(3*time.Second))
	}
	if len(history.Points) == 0 {
		t.Error("收敛后应包含样本")
	}

	// to 晚于当前时间：收敛到当前时刻。
	now := time.Now()
	future := m.SpeedHistory(SpeedHistoryQuery{
		From: now.Add(-time.Minute),
		To:   now.Add(time.Hour),
	})
	if future.To.After(time.Now()) {
		t.Errorf("To = %v，不应晚于当前时间", future.To)
	}

	// 查询范围完全在样本之后：返回空结果而不是旧的样本。
	outside := m.SpeedHistory(SpeedHistoryQuery{
		From: base.Add(time.Minute),
		To:   base.Add(2 * time.Minute),
	})
	if len(outside.Points) != 0 || outside.AverageSpeed != 0 || outside.PeakSpeed != 0 {
		t.Errorf("区间外查询 = %+v，期望空结果", outside)
	}
}

// TestResetSpeedStats 确认清空只重置平均/峰值，曲线点保留。
func TestResetSpeedStats(t *testing.T) {
	m := New(Config{Workers: 1})
	t.Cleanup(func() { m.Stop(context.Background()) })

	base := time.Unix(1763800000, 0)
	m.speedHistory.add(speedSample{at: base.Add(time.Second), speed: 100, running: 1})
	m.speedHistory.add(speedSample{at: base.Add(2 * time.Second), speed: 300, running: 1})

	query := SpeedHistoryQuery{
		From:   base,
		To:     base.Add(10 * time.Second),
		Bucket: time.Second,
	}
	before := m.SpeedHistory(query)
	if before.AverageSpeed != 200 || before.PeakSpeed != 300 {
		t.Fatalf("清空前汇总 = %d/%d，期望 200/300", before.AverageSpeed, before.PeakSpeed)
	}
	// from 早于最旧样本，会被收敛到第一个样本。
	if !before.StatsSince.Equal(base.Add(time.Second)) {
		t.Errorf("StatsSince = %v，期望 %v", before.StatsSince, base.Add(time.Second))
	}

	m.resetSpeedStatsAt(base.Add(3 * time.Second))

	after := m.SpeedHistory(query)
	if after.AverageSpeed != 0 || after.PeakSpeed != 0 {
		t.Errorf("清空后汇总 = %d/%d，期望 0/0", after.AverageSpeed, after.PeakSpeed)
	}
	if len(after.Points) != 2 {
		t.Errorf("清空后曲线点数 = %d，期望保留 2 个", len(after.Points))
	}
	if !after.StatsSince.Equal(base.Add(3 * time.Second)) {
		t.Errorf("清空后 StatsSince = %v，期望 %v", after.StatsSince, base.Add(3*time.Second))
	}

	// 清空后的新样本重新累计。
	m.speedHistory.add(speedSample{at: base.Add(4 * time.Second), speed: 500, running: 1})
	m.speedHistory.add(speedSample{at: base.Add(5 * time.Second), speed: 700, running: 1})
	next := m.SpeedHistory(query)
	if next.AverageSpeed != 600 || next.PeakSpeed != 700 {
		t.Errorf("新样本汇总 = %d/%d，期望 600/700", next.AverageSpeed, next.PeakSpeed)
	}
	if len(next.Points) != 4 {
		t.Errorf("新样本后曲线点数 = %d，期望 4 个", len(next.Points))
	}
}

// TestAutoSpeedBucket 确认自动粒度能覆盖常见时间窗并控制点数。
func TestAutoSpeedBucket(t *testing.T) {
	base := time.Unix(1763800000, 0)
	tests := []struct {
		span time.Duration
		want time.Duration
	}{
		{time.Minute, time.Second},
		{5 * time.Minute, 2 * time.Second},
		{30 * time.Minute, 10 * time.Second},
		{time.Hour, 15 * time.Second},
		{24 * time.Hour, 5 * time.Minute},
	}
	for _, tt := range tests {
		to := base.Add(tt.span)
		if got := autoSpeedBucket(base, to); got != tt.want {
			t.Errorf("autoSpeedBucket(%v) = %v，期望 %v", tt.span, got, tt.want)
		}
		if points := int(tt.span/autoSpeedBucket(base, to)) + 1; points > defaultSpeedHistoryPoints {
			t.Errorf("%v 窗口自动粒度点数 = %d，超过上限 %d",
				tt.span, points, defaultSpeedHistoryPoints)
		}
	}
}

// TestRecordSpeedSample 确认采样写入后可被历史查询读到。
func TestRecordSpeedSample(t *testing.T) {
	m := New(Config{Workers: 1})
	t.Cleanup(func() { m.Stop(context.Background()) })

	now := time.Now()
	m.recordSpeedSample(now)

	history := m.SpeedHistory(SpeedHistoryQuery{
		From: now.Add(-time.Minute),
		To:   now,
	})
	if len(history.Points) != 1 {
		t.Fatalf("采样点数 = %d，期望 1", len(history.Points))
	}
	if history.Points[0].Speed != 0 || history.Points[0].Running != 0 {
		t.Errorf("空载采样 = %+v，期望 0/0", history.Points[0])
	}
}
