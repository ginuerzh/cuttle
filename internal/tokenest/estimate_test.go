package tokenest

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
)

func TestEstimateAnthropicMinimumOne(t *testing.T) {
	cases := []string{
		`{}`,
		`{"model":"m","messages":[]}`,
		`{"model":"m","system":"","messages":[{"role":"user","content":""}]}`,
		`{"model":"m","messages":[{"role":"user","content":[]}],"tools":[]}`,
	}
	for _, body := range cases {
		n, err := EstimateAnthropic([]byte(body), DefaultSafetyFactor)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if n < 1 {
			t.Fatalf("%s: n = %d, want >= 1", body, n)
		}
	}
}

func TestEstimateAnthropicCountsMessagesAndTools(t *testing.T) {
	base := []byte(`{"model":"m","messages":[{"role":"user","content":"hello"}]}`)
	longer := []byte(`{"model":"m","messages":[{"role":"user","content":"hello hello hello hello hello hello hello hello"}]}`)
	withTool := []byte(`{"model":"m","messages":[{"role":"user","content":"hello"}],"tools":[{"name":"get_weather","description":"Get the current weather","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}]}`)

	nb, err := EstimateAnthropic(base, DefaultSafetyFactor)
	if err != nil {
		t.Fatal(err)
	}
	nl, err := EstimateAnthropic(longer, DefaultSafetyFactor)
	if err != nil {
		t.Fatal(err)
	}
	nt, err := EstimateAnthropic(withTool, DefaultSafetyFactor)
	if err != nil {
		t.Fatal(err)
	}
	if !(nb < nl && nl < nt) {
		t.Fatalf("estimates = %d, %d, %d; want base < longer < withTool", nb, nl, nt)
	}
}

func TestEstimateAnthropicCountsToolUseAndResults(t *testing.T) {
	without := []byte(`{"model":"m","messages":[{"role":"assistant","content":[{"type":"text","text":"ok"}]}]}`)
	with := []byte(`{"model":"m","messages":[{"role":"assistant","content":[{"type":"text","text":"ok"},{"type":"tool_use","id":"t","name":"get_weather","input":{"city":"San Francisco"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"sunny, 22C"}]}]}`)

	nw, err := EstimateAnthropic(without, DefaultSafetyFactor)
	if err != nil {
		t.Fatal(err)
	}
	n, err := EstimateAnthropic(with, DefaultSafetyFactor)
	if err != nil {
		t.Fatal(err)
	}
	if n < nw+10 {
		t.Fatalf("estimate with tool use = %d, without = %d; want at least +10", n, nw)
	}
}

func TestEstimateAnthropicImageAndDocument(t *testing.T) {
	text := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	image := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]}]}`)
	document := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hi"},{"type":"document","source":{"type":"text","media_type":"text/plain","data":"..."}}]}]}`)

	nt, err := EstimateAnthropic(text, DefaultSafetyFactor)
	if err != nil {
		t.Fatal(err)
	}
	ni, err := EstimateAnthropic(image, DefaultSafetyFactor)
	if err != nil {
		t.Fatal(err)
	}
	nd, err := EstimateAnthropic(document, DefaultSafetyFactor)
	if err != nil {
		t.Fatal(err)
	}
	if ni < nt+1600 {
		t.Fatalf("image estimate = %d, text = %d; want at least +1600", ni, nt)
	}
	if nd < nt+2000 {
		t.Fatalf("document estimate = %d, text = %d; want at least +2000", nd, nt)
	}
}

func TestEstimateAnthropicRejectsInvalidBody(t *testing.T) {
	for _, body := range []string{"", "not-json", "[]", `"text"`} {
		if _, err := EstimateAnthropic([]byte(body), DefaultSafetyFactor); err == nil {
			t.Fatalf("%q: expected an error", body)
		}
	}
}

func TestEstimateTextMonotonic(t *testing.T) {
	text := []rune("The quick brown fox 你好，世界")
	prev := estimateText("")
	for i := 1; i <= len(text); i++ {
		got := estimateText(string(text[:i]))
		if got < prev {
			t.Fatalf("estimate decreased at rune %d: %f < %f", i, got, prev)
		}
		prev = got
	}
}

func TestCalibration(t *testing.T) {
	f, err := os.Open("testdata/calibration.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var rows int
	var sumErr float64
	var under int
	for sc.Scan() {
		var row struct {
			Text   string `json:"text"`
			Tokens int    `json:"tokens"`
		}
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		got := int(math.Ceil(estimateText(row.Text) * DefaultSafetyFactor))
		if got < 1 {
			t.Fatalf("row %d: estimate = %d, want >= 1", rows, got)
		}
		if row.Tokens <= 0 {
			t.Fatalf("row %d: fixture tokens = %d", rows, row.Tokens)
		}
		sumErr += math.Abs(float64(got-row.Tokens)) / float64(row.Tokens)
		if got < row.Tokens {
			under++
		}
		rows++
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if rows != 141 {
		t.Fatalf("fixture rows = %d, want 141", rows)
	}
	if mape := sumErr / float64(rows); mape > 0.16 {
		t.Fatalf("MAPE = %.3f, want <= 0.16", mape)
	}
	if ratio := float64(under) / float64(rows); ratio > 0.08 {
		t.Fatalf("under-estimated = %.3f, want <= 0.08", ratio)
	}
}

func TestEstimateAnthropicRedactedThinking(t *testing.T) {
	without := []byte(`{"model":"m","messages":[{"role":"assistant","content":[{"type":"text","text":"ok"}]}]}`)
	data := strings.Repeat("A", 2000)
	with := []byte(`{"model":"m","messages":[{"role":"assistant","content":[{"type":"text","text":"ok"},{"type":"redacted_thinking","data":"` + data + `"}]}]}`)

	nw, err := EstimateAnthropic(without, DefaultSafetyFactor)
	if err != nil {
		t.Fatal(err)
	}
	n, err := EstimateAnthropic(with, DefaultSafetyFactor)
	if err != nil {
		t.Fatal(err)
	}
	if n < nw+100 {
		t.Fatalf("redacted_thinking estimate = %d, without = %d; want at least +100", n, nw)
	}
}
