package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"
)

// STTRecord is one line of eval/voice/stt.jsonl, downloaded from the test page's capture mode:
// what the person read out, and what the browser heard.
type STTRecord struct {
	ID         string `json:"id"`
	SpokenText string `json:"spoken_text"`
	Transcript string `json:"transcript"`
	Device     string `json:"device"`
}

// STTReport is speech-to-text accuracy, kept apart from extraction accuracy (D36).
type STTReport struct {
	Records    int
	ExactMatch float64 // normalised transcript == normalised spoken text
	WER        float64 // word error rate: total word edits / total reference words
}

// LoadSTT reads a JSONL file of STTRecords.
func LoadSTT(path string) ([]STTRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("eval: %w", err)
	}
	defer f.Close()
	var recs []STTRecord
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var r STTRecord
		if err := json.Unmarshal([]byte(text), &r); err != nil {
			return nil, fmt.Errorf("eval: %s:%d: %w", path, line, err)
		}
		if r.ID == "" || strings.TrimSpace(r.SpokenText) == "" {
			return nil, fmt.Errorf("eval: %s:%d: id and spoken_text are required", path, line)
		}
		recs = append(recs, r)
	}
	return recs, sc.Err()
}

// ScoreSTT compares each transcript with what was spoken.
func ScoreSTT(recs []STTRecord) STTReport {
	r := STTReport{Records: len(recs)}
	var exact, edits, words int
	for _, rec := range recs {
		ref, hyp := Words(rec.SpokenText), Words(rec.Transcript)
		if strings.Join(ref, " ") == strings.Join(hyp, " ") {
			exact++
		}
		edits += editDistance(ref, hyp)
		words += len(ref)
	}
	r.ExactMatch = frac(exact, r.Records)
	r.WER = frac(edits, words)
	return r
}

// Words normalises text for comparison, so only real recognition errors count:
//   - lower case; punctuation dropped (a decimal point between digits is kept: "12.5" is one word);
//   - English number words become digits ("twenty five" → "25", "twelve" → "12"), because STT
//     writing "20" for a spoken "twenty" is correct;
//   - unit spellings are unified: kilo/kilos/kgs/kilogram(s) → kg, pound(s)/lbs → lb.
//
// Hindi number words ("bara", "das") are left alone.
func Words(s string) []string {
	var b strings.Builder
	rs := []rune(strings.ToLower(s))
	for i, r := range rs {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '.' && i > 0 && i+1 < len(rs) && unicode.IsDigit(rs[i-1]) && unicode.IsDigit(rs[i+1]):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	var out []string
	words := strings.Fields(b.String())
	for i := 0; i < len(words); i++ {
		w := words[i]
		if u, ok := units[w]; ok {
			out = append(out, u)
			continue
		}
		if t, ok := tens[w]; ok {
			if i+1 < len(words) {
				if o, ok := ones[words[i+1]]; ok && o > 0 && o < 10 {
					out = append(out, strconv.Itoa(t+o))
					i++
					continue
				}
			}
			out = append(out, strconv.Itoa(t))
			continue
		}
		if o, ok := ones[w]; ok {
			out = append(out, strconv.Itoa(o))
			continue
		}
		out = append(out, w)
	}
	return out
}

var (
	ones = map[string]int{"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6,
		"seven": 7, "eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13,
		"fourteen": 14, "fifteen": 15, "sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19}
	tens = map[string]int{"twenty": 20, "thirty": 30, "forty": 40, "fifty": 50, "sixty": 60,
		"seventy": 70, "eighty": 80, "ninety": 90}
	units = map[string]string{"kilo": "kg", "kilos": "kg", "kgs": "kg", "kilogram": "kg", "kilograms": "kg",
		"pound": "lb", "pounds": "lb", "lbs": "lb"}
)

// editDistance is the word-level Levenshtein distance.
func editDistance(a, b []string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			sub := prev[j-1]
			if a[i-1] != b[j-1] {
				sub++
			}
			cur[j] = min(sub, prev[j]+1, cur[j-1]+1)
		}
		prev = cur
	}
	return prev[len(b)]
}
