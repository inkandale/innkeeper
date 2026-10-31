package protocol

import (
	"encoding/json"
	"testing"
)

func TestServiceCallHeaderReadsFromAChatRequest(t *testing.T) {
	raw, err := json.Marshal(AIChatParams{ServiceCall: ServiceCall{Instance: 9, Run: "mine-1"},
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var head ServiceCall
	if err := json.Unmarshal(raw, &head); err != nil {
		t.Fatal(err)
	}
	if head != (ServiceCall{Instance: 9, Run: "mine-1"}) {
		t.Fatalf("header = %+v from %s", head, raw)
	}
}

func TestToolCallExtraRoundTripsThroughJSON(t *testing.T) {
	in := `{"id":"c1","name":"look","arguments":"{}","extra":{"thought_signature":"c2ln"}}`
	var call ToolCall
	if err := json.Unmarshal([]byte(in), &call); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(call)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != in {
		t.Fatalf("round trip = %s, want %s", out, in)
	}
}

func TestEveryServiceHasASample(t *testing.T) {
	for method := range Services {
		if s, ok := ServiceSamples[method]; !ok || s.Params == nil || s.Check == nil {
			t.Errorf("%s has no sample in ServiceSamples", method)
		}
	}
}

func TestServiceSamplesCheckTheReplyShape(t *testing.T) {
	tests := []struct {
		method, reply string
		wantOK        bool
	}{
		{MethodAIChat, `{"text": "OK"}`, true},
		{MethodAIChat, `{"text": ""}`, false},
		{MethodAIChat, `{"tool_calls": [{"name": "look"}]}`, false},
		{MethodAIEmbed, `{"vectors": [[0.1, 0.2]]}`, true},
		{MethodAIEmbed, `{"vectors": []}`, false},
		{MethodAIEmbed, `{"vectors": [[]]}`, false},
		{MethodAIDecide, `{"answers": {}}`, false},
		{MethodAIDecide, `{"answers": {"greeting": {"type": "score"}}}`, false},
		{MethodAIDecide, `{"answers": {"greeting": {"type": "noul"}}}`, false},
		{MethodAIDecide, `{"answers": {"greeting": {"type": "noul", "noul": 1}}}`, true},
		{MethodVectorQuery, `{"space": "m1", "vector": [0.1, 0.2]}`, true},
		{MethodVectorQuery, `{}`, false},
		{MethodVectorQuery, `null`, false},
	}
	for _, tt := range tests {
		err := ServiceSamples[tt.method].Check(json.RawMessage(tt.reply))
		if (err == nil) != tt.wantOK {
			t.Errorf("%s %s: got %v, want ok %v", tt.method, tt.reply, err, tt.wantOK)
		}
	}
}
