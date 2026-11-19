package websocket

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCallPayloadParsing(t *testing.T) {
	offerJSON := []byte(`{
		"type": "call_offer",
		"call_id": "test_call_123",
		"receiver_id": 42,
		"call_type": "video",
		"sdp": {"type": "offer", "sdp": "v=0\r\no=..."}
	}`)

	var offerPayload CallOfferPayload
	if err := json.Unmarshal(offerJSON, &offerPayload); err != nil {
		t.Fatalf("Failed to parse CallOfferPayload: %v", err)
	}
	if offerPayload.CallID != "test_call_123" {
		t.Errorf("expected call_id 'test_call_123', got %s", offerPayload.CallID)
	}
	if offerPayload.ReceiverID != 42 {
		t.Errorf("expected receiver_id 42, got %d", offerPayload.ReceiverID)
	}
	if offerPayload.CallType != "video" {
		t.Errorf("expected call_type 'video', got %s", offerPayload.CallType)
	}
	if len(offerPayload.SDP) == 0 {
		t.Errorf("expected SDP raw message, got empty")
	}

	answerJSON := []byte(`{
		"type": "call_answer",
		"call_id": "test_call_123",
		"caller_id": 10,
		"sdp": {"type": "answer", "sdp": "v=0\r\no=..."}
	}`)

	var answerPayload CallAnswerPayload
	if err := json.Unmarshal(answerJSON, &answerPayload); err != nil {
		t.Fatalf("Failed to parse CallAnswerPayload: %v", err)
	}
	if answerPayload.CallID != "test_call_123" {
		t.Errorf("expected call_id 'test_call_123', got %s", answerPayload.CallID)
	}
	if answerPayload.CallerID != 10 {
		t.Errorf("expected caller_id 10, got %d", answerPayload.CallerID)
	}

	iceJSON := []byte(`{
		"type": "ice_candidate",
		"to": 10,
		"candidate": {"candidate": "candidate:1 1 UDP ...", "sdpMid": "0"}
	}`)

	var icePayload ICECandidatePayload
	if err := json.Unmarshal(iceJSON, &icePayload); err != nil {
		t.Fatalf("Failed to parse ICECandidatePayload: %v", err)
	}
	if icePayload.To != 10 {
		t.Errorf("expected to 10, got %d", icePayload.To)
	}

	rejectJSON := []byte(`{
		"type": "call_reject",
		"caller_id": 10,
		"reason": "busy"
	}`)

	var rejectPayload CallRejectPayload
	if err := json.Unmarshal(rejectJSON, &rejectPayload); err != nil {
		t.Fatalf("Failed to parse CallRejectPayload: %v", err)
	}
	if rejectPayload.Reason != "busy" {
		t.Errorf("expected reason 'busy', got %s", rejectPayload.Reason)
	}

	endJSON := []byte(`{
		"type": "call_end",
		"receiver_id": 42
	}`)

	var endPayload CallEndPayload
	if err := json.Unmarshal(endJSON, &endPayload); err != nil {
		t.Fatalf("Failed to parse CallEndPayload: %v", err)
	}
	if endPayload.ReceiverID != 42 {
		t.Errorf("expected receiver_id 42, got %d", endPayload.ReceiverID)
	}
}

func TestActiveCallDurationCalculation(t *testing.T) {
	acceptedTime := time.Now().Add(-15 * time.Second)
	session := &CallSession{
		CallerID:   1,
		ReceiverID: 2,
		CallType:   "video",
		StartedAt:  acceptedTime.Add(-2 * time.Second),
		AcceptedAt: &acceptedTime,
	}

	duration := int(time.Since(*session.AcceptedAt).Seconds())
	if duration < 15 {
		t.Errorf("expected duration >= 15 seconds, got %d", duration)
	}
}

func TestICECandidateRelayNoDB(t *testing.T) {
	candidateData := json.RawMessage(`{"candidate":"candidate:2 1 UDP 2122260223 192.168.1.5 50000 typ host","sdpMid":"0","sdpMLineIndex":0}`)
	relay := ICECandidateRelay{
		Type:      "ice_candidate",
		CallID:    "test_call_999",
		SenderID:  5,
		Candidate: candidateData,
	}

	bytes, err := json.Marshal(relay)
	if err != nil {
		t.Fatalf("Failed to marshal ICECandidateRelay: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal ICECandidateRelay JSON: %v", err)
	}

	if parsed["type"] != "ice_candidate" {
		t.Errorf("expected type 'ice_candidate', got %v", parsed["type"])
	}
	if parsed["sender_id"] != float64(5) {
		t.Errorf("expected sender_id 5, got %v", parsed["sender_id"])
	}
	if parsed["candidate"] == nil {
		t.Errorf("expected candidate payload to be preserved")
	}
}

func TestCallEndRelay(t *testing.T) {
	relay := CallEndRelay{
		Type:            "call_end",
		CallID:          "test_call_777",
		SenderID:        3,
		DurationSeconds: 45,
	}

	bytes, err := json.Marshal(relay)
	if err != nil {
		t.Fatalf("Failed to marshal CallEndRelay: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(bytes, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal CallEndRelay JSON: %v", err)
	}

	if parsed["type"] != "call_end" {
		t.Errorf("expected type 'call_end', got %v", parsed["type"])
	}
	if parsed["duration_seconds"] != float64(45) {
		t.Errorf("expected duration_seconds 45, got %v", parsed["duration_seconds"])
	}
}
