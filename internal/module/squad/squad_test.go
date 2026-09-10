package squad

import "testing"

// 状态机迁移矩阵全排列测试（详细设计 §7）。
func TestCanTransitionMatrix(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		// 合法 8 迁移
		{StIDLE, StPOOLED, true},
		{StIDLE, StEXPIRED, true},
		{StPOOLED, StNEGOTIATING, true},
		{StPOOLED, StEXPIRED, true},
		{StNEGOTIATING, StCONFIRMED, true},
		{StNEGOTIATING, StEXPIRED, true},
		{StCONFIRMED, StSETTLED, true},
		{StSETTLED, StREVIEWED, true},
		// 非法（跳跃/回退）
		{StIDLE, StCONFIRMED, false},
		{StNEGOTIATING, StSETTLED, false},
		{StCONFIRMED, StNEGOTIATING, false},
		{StSETTLED, StCONFIRMED, false},
		{StREVIEWED, StIDLE, false},
		{StEXPIRED, StPOOLED, false},
		{StSETTLED, StEXPIRED, false},
	}
	for _, c := range cases {
		if got := CanTransition(c.from, c.to); got != c.want {
			t.Errorf("CanTransition(%s→%s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

// 终态不可再迁移。
func TestTerminalStates(t *testing.T) {
	for _, st := range []string{StREVIEWED, StEXPIRED} {
		if CanTransition(st, StIDLE) || CanTransition(st, StCONFIRMED) {
			t.Errorf("terminal state %s should not transition", st)
		}
	}
}
