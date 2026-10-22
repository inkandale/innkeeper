// Package cardhash computes the duplicate-detection fingerprints for cards
// and lorebooks. Catalog plugins import it so their hashes match the
// library's; the test vectors in this package pin each algorithm.
package cardhash

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash/fnv"
	"math/bits"
	"strings"
	"unicode"
)

// Fields that change on every save without changing the card.
var volatile = []string{"creation_date", "modification_date"}

// appLocalKeys are extensions that hold SillyTavern's favorite star and world
// link: app state, not card content.
var appLocalKeys = []string{"fav", "world"}

// CardData returns a CCv3 card's data object as the library stores and
// hashes it, without character_book, the app-local extensions, or a tag that
// repeats an earlier one in another case, and the character_book, or nil.
// data is nil when the card has no data.
func CardData(card json.RawMessage) (data, book json.RawMessage, err error) {
	var c struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(card, &c); err != nil || c.Data == nil {
		return nil, nil, err
	}
	if b := c.Data["character_book"]; len(b) > 0 && string(b) != "null" {
		book = b
	}
	delete(c.Data, "character_book")
	var ext map[string]json.RawMessage
	if json.Unmarshal(c.Data["extensions"], &ext) == nil && ext != nil {
		for _, k := range appLocalKeys {
			delete(ext, k)
		}
		if c.Data["extensions"], err = json.Marshal(ext); err != nil {
			return nil, nil, err
		}
	}
	if tags, ok := c.Data["tags"]; ok {
		if c.Data["tags"], err = UniqueTags(tags); err != nil {
			return nil, nil, err
		}
	}
	data, err = json.Marshal(c.Data)
	return data, book, err
}

// UniqueTags drops each string tag an earlier one matches in any case, so
// the library's tag counts see a card once per tag. A non-list, or a list
// with no repeats, comes back as is.
func UniqueTags(raw json.RawMessage) (json.RawMessage, error) {
	var tags []json.RawMessage
	if json.Unmarshal(raw, &tags) != nil {
		return raw, nil
	}
	seen := map[string]bool{}
	var kept []json.RawMessage
	for _, t := range tags {
		var s string
		if json.Unmarshal(t, &s) == nil {
			if seen[strings.ToLower(s)] {
				continue
			}
			seen[strings.ToLower(s)] = true
		}
		kept = append(kept, t)
	}
	if len(kept) == len(tags) {
		return raw, nil
	}
	return json.Marshal(kept)
}

// ContentHash returns the hex SHA-256 of the card or lorebook data in
// canonical form:
//
//   - top-level creation_date and modification_date removed
//   - every string with CRLF replaced by LF and surrounding whitespace trimmed
//   - object members that are then null, "", [], or {} removed
//   - compact JSON with object keys sorted and no HTML escaping, which matches
//     Python's json.dumps(v, sort_keys=True, separators=(",", ":"), ensure_ascii=False)
//
// ponytail: numbers keep their original text and Go escapes U+2028/U+2029,
// so a Python port can differ on those; normalize numbers if that matters.
func ContentHash(data []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", err
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return "", errors.New("data must be a JSON object")
	}
	for _, k := range volatile {
		delete(obj, k)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(normalize(obj)); err != nil {
		return "", err
	}
	sum := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	return hex.EncodeToString(sum[:]), nil
}

func normalize(v any) any {
	switch v := v.(type) {
	case string:
		return strings.TrimSpace(strings.ReplaceAll(v, "\r\n", "\n"))
	case []any:
		for i := range v {
			v[i] = normalize(v[i])
		}
	case map[string]any:
		for k, x := range v {
			if x = normalize(x); isEmpty(x) {
				delete(v, k)
			} else {
				v[k] = x
			}
		}
	}
	return v
}

func isEmpty(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}

// CardSimhash returns the Simhash of a card's description, personality,
// scenario, and first_mes, joined with newlines. ok is false when they hold no
// words.
func CardSimhash(data []byte) (hash int64, ok bool, err error) {
	var c struct {
		Description string `json:"description"`
		Personality string `json:"personality"`
		Scenario    string `json:"scenario"`
		FirstMes    string `json:"first_mes"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return 0, false, err
	}
	hash, ok = Simhash(strings.Join([]string{c.Description, c.Personality, c.Scenario, c.FirstMes}, "\n"))
	return hash, ok, nil
}

// LorebookSimhash returns the Simhash of a lorebook's entry contents, joined
// with newlines in entry order.
func LorebookSimhash(data []byte) (hash int64, ok bool, err error) {
	var b struct {
		Entries []struct {
			Content string `json:"content"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &b); err != nil {
		return 0, false, err
	}
	parts := make([]string, len(b.Entries))
	for i, e := range b.Entries {
		parts[i] = e.Content
	}
	hash, ok = Simhash(strings.Join(parts, "\n"))
	return hash, ok, nil
}

// Simhash returns a 64-bit Charikar simhash of text. Words are lowercased runs
// of letters and digits. Each run of 3 consecutive words (or all the words, if
// there are fewer) is hashed with FNV-1a 64 over the words joined by one
// space. Bit b of the result is set when more shingles have bit b set than
// clear. ok is false when text has no words.
func Simhash(text string) (hash int64, ok bool) {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	if len(words) == 0 {
		return 0, false
	}
	n := min(3, len(words))
	var counts [64]int
	for i := 0; i+n <= len(words); i++ {
		h := fnv.New64a()
		h.Write([]byte(strings.Join(words[i:i+n], " ")))
		x := h.Sum64()
		for b := range counts {
			if x>>b&1 == 1 {
				counts[b]++
			} else {
				counts[b]--
			}
		}
	}
	var out uint64
	for b, c := range counts {
		if c > 0 {
			out |= 1 << b
		}
	}
	return int64(out), true
}

// Distance is the Hamming distance between two 64-bit hashes.
func Distance(a, b int64) int {
	return bits.OnesCount64(uint64(a ^ b))
}
