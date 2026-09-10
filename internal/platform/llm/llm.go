// Package llm LLM 平台接口与 mock 实现（详细设计 §2.4）。
//
// 降级链：mock → hunyuan → gpt。初版默认 mock（时间窗交集 + 费用模板，延迟 1~2s）。
package llm

import (
	"context"
	"encoding/json"
	"math/rand"
	"time"
)

// TimeWindowResolver 协商引擎依赖的 LLM 能力：给定双方偏好，产出方案卡。
type TimeWindowResolver interface {
	Resolve(ctx context.Context, in IntentPair) (Proposal, error)
}

// IntentPair 协商输入：双方意图快照。
type IntentPair struct {
	Initiator Intent `json:"initiator"`
	Candidate Intent `json:"candidate"`
}

// Intent 单方意图（时间窗/费用模式/角色偏好）。
type Intent struct {
	UserID   string   `json:"user_id"`
	Activity string   `json:"activity"`
	Hours    []int    `json:"hours"`     // 活跃时段（0-23）
	Weekdays []string `json:"weekdays"` // MON..SUN
	CostMode string   `json:"cost_mode"` // AA | ROTATE | TREAT
}

// Proposal LLM 方案卡（进入 negotiation_rounds.proposal）。
type Proposal struct {
	Time     string  `json:"time"`      // 例 "FRI 20:00-22:00"
	Venue    string  `json:"venue"`     // 场地建议
	CostMode string  `json:"cost_mode"` // 推荐费用模式
	PerHead  float64 `json:"per_head"`  // 人均（元）
	Reason   string  `json:"reason"`    // 推荐理由（一句话）
}

// Mock 初版实现：本地规则求解，无外部调用。
type Mock struct{}

// NewMock 构造 mock resolver。
func NewMock() *Mock { return &Mock{} }

// Resolve 时间窗取交集 + 费用模板；模拟 LLM 1~2s 延迟。
func (m *Mock) Resolve(ctx context.Context, in IntentPair) (Proposal, error) {
	select {
	case <-ctx.Done():
		return Proposal{}, ctx.Err()
	case <-time.After(time.Duration(1000+rand.Intn(1000)) * time.Millisecond):
	}

	// 时间交集
	var hours []int
	ih := map[int]bool{}
	for _, h := range in.Initiator.Hours {
		ih[h] = true
	}
	for _, h := range in.Candidate.Hours {
		if ih[h] {
			hours = append(hours, h)
		}
	}
	var week string
	if len(hours) == 0 {
		hours = []int{20, 21}
	}
	if wd := in.Candidate.Weekdays; len(wd) > 0 {
		week = wd[0]
	} else {
		week = "FRI"
	}

	// 费用模式：双方一致取之；否则 AA 兜底
	mode := "AA"
	if in.Initiator.CostMode == in.Candidate.CostMode {
		mode = in.Initiator.CostMode
	}

	return Proposal{
		Time:     formatWindow(week, hours),
		Venue:    "徐汇滨江羽毛球馆",
		CostMode: mode,
		PerHead:  42.0,
		Reason:   "时间窗重合，消费观一致，AI 已按双方习惯生成方案",
	}, nil
}

func formatWindow(week string, hours []int) string {
	if len(hours) == 0 {
		return week + " 20:00-22:00"
	}
	min, max := hours[0], hours[0]
	for _, h := range hours {
		if h < min {
			min = h
		}
		if h > max {
			max = h
		}
	}
	return week + " " + pad(min) + ":00-" + pad(max+1) + ":00"
}

func pad(n int) string {
	b, _ := json.Marshal(n)
	if n < 10 {
		return "0" + string(b)
	}
	return string(b)
}
