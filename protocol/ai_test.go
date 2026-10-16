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
