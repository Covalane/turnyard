package agents

import "testing"

func TestNeedsInputQuestion(t *testing.T) {
	tests := []struct {
		name  string
		texts []string
		want  string
	}{
		{"final question", []string{"Done.\nTURNYARD_NEEDS_INPUT: 请确认采用哪种授权方式？"}, "请确认采用哪种授权方式？"},
		{"negative marker", []string{"Done.\nTURNYARD_NEEDS_INPUT: 无 — 任务已完成"}, ""},
		{"question beginning with no", []string{"TURNYARD_NEEDS_INPUT: 无法确定目标分支，请确认使用 main 还是 release？"}, "无法确定目标分支，请确认使用 main 还是 release？"},
		{"earlier mention", []string{"TURNYARD_NEEDS_INPUT: 需要选择吗？", "任务完成。"}, ""},
		{"quoted in explanation", []string{"The marker TURNYARD_NEEDS_INPUT: is only used for questions."}, ""},
		{"empty", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NeedsInputQuestion(tt.texts); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
