package anthropic

import (
	"context"
	"strings"
	"testing"
)

// v2.8.15 协议层 fix-p1：计费头 cch 动态字段剥离（保住上游 prompt cache）。
func TestStripCchFromBillingHeader(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "非计费头原样返回",
			in:   "You are a helpful assistant.",
			want: "You are a helpful assistant.",
		},
		{
			name: "中段 cch 剥离且保留后续字段",
			in:   "x-anthropic-billing-header: cc_version=2.0.30; cch=abc123; cc_entrypoint=cli;",
			want: "x-anthropic-billing-header: cc_version=2.0.30; cc_entrypoint=cli;",
		},
		{
			name: "末尾 cch 无尾分号",
			in:   "x-anthropic-billing-header: cc_version=2.0.30; cch=abc123",
			want: "x-anthropic-billing-header: cc_version=2.0.30",
		},
		{
			name: "多次出现全部剥离",
			in:   "x-anthropic-billing-header: cc_version=1; cch=aaa; cch=bbb;",
			want: "x-anthropic-billing-header: cc_version=1;",
		},
		{
			name: "无 cch 时不变",
			in:   "x-anthropic-billing-header: cc_version=2.0.30; cc_entrypoint=cli;",
			want: "x-anthropic-billing-header: cc_version=2.0.30; cc_entrypoint=cli;",
		},
		{
			name: "首部空白与大小写容忍",
			in:   "\n  X-Anthropic-Billing-Header: cc_version=2.0.30; cch=abc123;",
			want: "\n  X-Anthropic-Billing-Header: cc_version=2.0.30;",
		},
		{
			name: "CCH 大写同样剥离",
			in:   "x-anthropic-billing-header: cc_version=1; CCH=abc;",
			want: "x-anthropic-billing-header: cc_version=1;",
		},
	}
	for _, tc := range cases {
		if got := stripCchFromBillingHeader(tc.in); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// 数组格式：计费头文本块剥离、普通 system 块不受影响；其余消息不动。
func TestMessagesInboundStripsCchFromBillingHeaderArrayFormat(t *testing.T) {
	body := []byte(`{
	  "model": "claude-sonnet-4-5",
	  "max_tokens": 64,
	  "system": [
	    {"type": "text", "text": "x-anthropic-billing-header: cc_version=2.0.30; cc_entrypoint=cli; cch=deadbeef;"},
	    {"type": "text", "text": "You are CHOIR."}
	  ],
	  "messages": [{"role": "user", "content": "hi"}]
	}`)

	adapter := &MessagesInbound{}
	req, err := adapter.TransformRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("TransformRequest error = %v", err)
	}

	var sys []string
	for _, m := range req.Messages {
		if m.Role == "system" && m.Content.Content != nil {
			sys = append(sys, *m.Content.Content)
		}
	}
	if len(sys) != 2 {
		t.Fatalf("system 块数 = %d, want 2 (%v)", len(sys), sys)
	}
	if strings.Contains(sys[0], "cch=") {
		t.Fatalf("计费头 cch 未剥离：%q", sys[0])
	}
	if sys[0] != "x-anthropic-billing-header: cc_version=2.0.30; cc_entrypoint=cli;" {
		t.Fatalf("计费头剥离结果异常：%q", sys[0])
	}
	if sys[1] != "You are CHOIR." {
		t.Fatalf("普通 system 块被改动：%q", sys[1])
	}
}

// 字符串格式的 system 同样剥离。
func TestMessagesInboundStripsCchFromBillingHeaderStringFormat(t *testing.T) {
	body := []byte(`{
	  "model": "claude-sonnet-4-5",
	  "max_tokens": 64,
	  "system": "x-anthropic-billing-header: cc_version=2.0.30; cch=deadbeef; cc_entrypoint=cli;",
	  "messages": [{"role": "user", "content": "hi"}]
	}`)

	adapter := &MessagesInbound{}
	req, err := adapter.TransformRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("TransformRequest error = %v", err)
	}

	var system *string
	for i := range req.Messages {
		if req.Messages[i].Role == "system" {
			system = req.Messages[i].Content.Content
			break
		}
	}
	if system == nil {
		t.Fatal("system 消息缺失")
	}
	if *system != "x-anthropic-billing-header: cc_version=2.0.30; cc_entrypoint=cli;" {
		t.Fatalf("字符串格式剥离结果异常：%q", *system)
	}
}
