package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func testManager(p Policy) *Manager {
	m := NewManager(p)
	m.now = func() time.Time { return time.Unix(1000, 0) }
	return m
}

func TestPolicyModes(t *testing.T) {
	off := testManager(Policy{Mode: "off", MaxBytes: 64})
	if _, ok := off.Start("s1", "r", "m"); ok {
		t.Fatal("off mode captured")
	}

	errs := testManager(Policy{Mode: "errors", MaxBytes: 64})
	reason, ok := errs.Start("s1", "r", "m")
	if !ok || reason != "mode:errors" {
		t.Fatalf("errors mode = %q/%v", reason, ok)
	}
	cap, ok := errs.Finish("s1")
	if !ok || !cap.OnError {
		t.Fatalf("errors capture = %+v/%v", cap, ok)
	}

	all := testManager(Policy{Mode: "sample", SampleRate: 1, MaxBytes: 64})
	if _, ok := all.Start("s1", "r", "m"); !ok {
		t.Fatal("sampleRate 1 did not capture")
	}
	none := testManager(Policy{Mode: "sample", SampleRate: 0, MaxBytes: 64})
	if _, ok := none.Start("s1", "r", "m"); ok {
		t.Fatal("sampleRate 0 captured")
	}
}

func TestRuleMatchGlobLimitTTL(t *testing.T) {
	m := testManager(Policy{Mode: "off", MaxBytes: 64})
	rule, err := m.AddRule("fast", "claude-*", "investigate", 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if reason, ok := m.Start("s1", "fast", "CLAUDE-sonnet-4-5"); !ok || reason != "rule:"+rule.ID {
		t.Fatalf("glob match = %q/%v", reason, ok)
	}
	if _, ok := m.Start("s2", "fast", "gpt-4.5"); ok {
		t.Fatal("non-matching client model captured")
	}
	if _, ok := m.Start("s3", "slow", "claude-sonnet-4-5"); ok {
		t.Fatal("non-matching route captured")
	}
	if _, ok := m.Start("s4", "fast", "claude-opus"); ok {
		t.Fatal("limit was not enforced")
	}

	m.now = func() time.Time { return time.Unix(1000+3601, 0) }
	if _, ok := m.Start("s5", "fast", "claude-sonnet-4-5"); ok {
		t.Fatal("expired rule captured")
	}
}

func TestOldestRuleWins(t *testing.T) {
	m := testManager(Policy{Mode: "off", MaxBytes: 64})
	first, err := m.AddRule("", "", "first", 10, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.AddRule("fast", "claude-*", "second", 10, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	reason, ok := m.Start("s1", "fast", "claude-sonnet-4-5")
	if !ok || reason != "rule:"+first.ID {
		t.Fatalf("winner = %q/%v, want oldest %s", reason, ok, first.ID)
	}
	rules := m.Rules()
	if len(rules) != 2 || rules[0].Used != 1 || rules[1].Used != 0 {
		t.Fatalf("rules = %+v", rules)
	}
	_ = second
}

func TestSSEFirstLastCount(t *testing.T) {
	m := testManager(Policy{Mode: "errors", MaxBytes: 64})
	if _, ok := m.Start("s1", "r", "m"); !ok {
		t.Fatal("not captured")
	}
	m.Offer("s1", "response", []byte("o1"), []byte("r1"), "start", 0, 0)
	m.Offer("s1", "response", []byte("o2"), []byte("r2"), "event", 1, 0)
	m.Offer("s1", "response", []byte("o3"), []byte("r3"), "event", 2, 0)
	cap, ok := m.Finish("s1")
	if !ok {
		t.Fatal("finish failed")
	}
	if cap.SSIEvents != 3 {
		t.Fatalf("SSIEvents = %d", cap.SSIEvents)
	}
	if cap.SSEFirstOriginal != "o1" || cap.SSEFirstRewritten != "r1" {
		t.Fatalf("first = %+v", cap)
	}
	if cap.SSELastOriginal != "o3" || cap.SSELastRewritten != "r3" {
		t.Fatalf("last = %+v", cap)
	}
}

func TestPreviewsTruncated(t *testing.T) {
	m := testManager(Policy{Mode: "errors", MaxBytes: 4})
	if _, ok := m.Start("s1", "r", "m"); !ok {
		t.Fatal("not captured")
	}
	m.Offer("s1", "request", []byte("abcdef"), []byte("ghijkl"), "", 0, 0)
	cap, _ := m.Finish("s1")
	if cap.RequestOriginal != "abcd" || cap.RequestRewritten != "ghij" {
		t.Fatalf("previews = %+v", cap)
	}
	want := sha256.Sum256([]byte("ghijkl"))
	if cap.RequestSHA256 != hex.EncodeToString(want[:]) {
		t.Fatalf("hash = %q", cap.RequestSHA256)
	}
	if len(cap.TruncatedFields) != 2 ||
		cap.TruncatedFields[0] != FieldRequestOriginal ||
		cap.TruncatedFields[1] != FieldRequestRewritten {
		t.Fatalf("truncatedFields = %+v", cap.TruncatedFields)
	}
}

func TestExactLimitNotTruncated(t *testing.T) {
	m := testManager(Policy{Mode: "errors", MaxBytes: 6})
	if _, ok := m.Start("s1", "r", "m"); !ok {
		t.Fatal("not captured")
	}
	m.Offer("s1", "request", []byte("abcdef"), []byte("ghijkl"), "", 0, 0)
	cap, _ := m.Finish("s1")
	if len(cap.TruncatedFields) != 0 {
		t.Fatalf("truncatedFields = %+v, want none", cap.TruncatedFields)
	}
}

func TestSecondChunkMarksTruncated(t *testing.T) {
	m := testManager(Policy{Mode: "errors", MaxBytes: 8})
	if _, ok := m.Start("s1", "r", "m"); !ok {
		t.Fatal("not captured")
	}
	m.Offer("s1", "request", []byte("abcd"), []byte("efgh"), "", 0, 0)
	m.Offer("s1", "request", []byte("efghij"), []byte("klmnop"), "", 0, 0)
	cap, _ := m.Finish("s1")
	if cap.RequestOriginal != "abcdefgh" {
		t.Fatalf("requestOriginal = %q", cap.RequestOriginal)
	}
	if len(cap.TruncatedFields) != 2 ||
		cap.TruncatedFields[0] != FieldRequestOriginal ||
		cap.TruncatedFields[1] != FieldRequestRewritten {
		t.Fatalf("truncatedFields = %+v", cap.TruncatedFields)
	}
}

func TestNewAttemptReplacesPreviousCapture(t *testing.T) {
	m := testManager(Policy{Mode: "errors", MaxBytes: 64})
	if _, ok := m.Start("s1", "r", "m"); !ok {
		t.Fatal("not captured")
	}
	m.Offer("s1", "request", []byte("a"), []byte("ra"), "", 0, 0)
	m.Offer("s1", "response", []byte("o"), []byte("ro"), "", 0, 0)
	m.Offer("s1", "request", []byte("a"), []byte("rb"), "", 0, 1)
	cap, ok := m.Finish("s1")
	if !ok {
		t.Fatal("finish failed")
	}
	if cap.RequestRewritten != "rb" {
		t.Fatalf("requestRewritten = %q, want rb", cap.RequestRewritten)
	}
	if cap.ResponseRewritten != "" {
		t.Fatalf("responseRewritten = %q, want empty", cap.ResponseRewritten)
	}
	want := sha256.Sum256([]byte("rb"))
	if cap.RequestSHA256 != hex.EncodeToString(want[:]) {
		t.Fatalf("requestSha256 = %q", cap.RequestSHA256)
	}
	if m.totalBytes != 0 {
		t.Fatalf("totalBytes = %d, want 0 after finish", m.totalBytes)
	}
}

func TestFailedRewriteKeepsOriginal(t *testing.T) {
	m := testManager(Policy{Mode: "errors", MaxBytes: 64})
	if _, ok := m.Start("s1", "r", "m"); !ok {
		t.Fatal("not captured")
	}
	m.Offer("s1", "response", []byte("original"), nil, "", 0, 0)
	cap, _ := m.Finish("s1")
	if cap.ResponseOriginal != "original" || cap.ResponseRewritten != "" {
		t.Fatalf("capture = %+v", cap)
	}
}

func TestFinishRemovesSession(t *testing.T) {
	m := testManager(Policy{Mode: "errors", MaxBytes: 64})
	if _, ok := m.Start("s1", "r", "m"); !ok {
		t.Fatal("not captured")
	}
	if _, ok := m.Finish("s1"); !ok {
		t.Fatal("first finish failed")
	}
	if _, ok := m.Finish("s1"); ok {
		t.Fatal("second finish returned a capture")
	}
}

func TestSSELastIgnoresSyntheticEnd(t *testing.T) {
	m := testManager(Policy{Mode: "errors", MaxBytes: 64})
	if _, ok := m.Start("s1", "r", "m"); !ok {
		t.Fatal("not captured")
	}
	m.Offer("s1", "response", []byte("o1"), []byte("r1"), "start", 0, 0)
	m.Offer("s1", "response", []byte("o2"), []byte("r2"), "event", 1, 0)
	m.Offer("s1", "response", nil, []byte("message_stop"), "end", 2, 0)
	cap, _ := m.Finish("s1")
	if cap.SSELastOriginal != "o2" || cap.SSELastRewritten != "r2" {
		t.Fatalf("last = %q/%q", cap.SSELastOriginal, cap.SSELastRewritten)
	}
	if cap.SSIEvents != 2 {
		t.Fatalf("events = %d", cap.SSIEvents)
	}
}

func TestRuleReasonInCapture(t *testing.T) {
	m := testManager(Policy{Mode: "off", MaxBytes: 64})
	if _, err := m.AddRule("fast", "claude-*", "investigate", 10, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Start("s1", "fast", "claude-sonnet"); !ok {
		t.Fatal("not captured")
	}
	cap, _ := m.Finish("s1")
	if cap.Reason != "investigate" {
		t.Fatalf("reason = %q", cap.Reason)
	}
}
