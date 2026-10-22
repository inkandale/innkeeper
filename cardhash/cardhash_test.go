package cardhash

import "testing"

// Vectors computed independently in Python, as the archive plugin will.
const (
	card = `{"name": "Brakka", "description": "A grumpy dwarf blacksmith.\r\n",
		"first_mes": "  Hm? <What> do you want & why?  ", "tags": ["dwarf", "smith"],
		"alternate_greetings": [], "creator_notes": "", "extensions": {"depth": 4, "x": null},
		"creation_date": 1700000000}`
	cardContentHash = "90b4d55b00d60bc47f6ae0caad4cdcbe0ded0c6a789d128ef15016e9ad14a1e0"
	cardSimhash     = -6846279246937013872
)

func TestContentHashVector(t *testing.T) {
	got, err := ContentHash([]byte(card))
	if err != nil {
		t.Fatal(err)
	}
	if got != cardContentHash {
		t.Fatalf("got %s, want %s", got, cardContentHash)
	}
}

func TestContentHashIgnoresFormatting(t *testing.T) {
	a, _ := ContentHash([]byte(`{"name": "A", "description": "x", "modification_date": 1}`))
	b, _ := ContentHash([]byte(`{"description":" x\n","personality":"","name":"A"}`))
	if a != b {
		t.Fatalf("hashes differ: %s, %s", a, b)
	}
	c, _ := ContentHash([]byte(`{"name": "A", "description": "y"}`))
	if a == c {
		t.Fatal("different descriptions hash the same")
	}
}

func TestContentHashRejectsNonObject(t *testing.T) {
	if _, err := ContentHash([]byte(`[1]`)); err == nil {
		t.Fatal("accepted an array")
	}
}

func TestSimhashVectors(t *testing.T) {
	got, ok, err := CardSimhash([]byte(card))
	if err != nil || !ok {
		t.Fatalf("CardSimhash: %v, ok=%v", err, ok)
	}
	if got != cardSimhash {
		t.Errorf("CardSimhash = %d, want %d", got, int64(cardSimhash))
	}
	if got, _ := Simhash("Hello, World"); got != 8618312879776256743 {
		t.Errorf("Simhash(short) = %d", got)
	}
	if _, ok := Simhash(" ... "); ok {
		t.Error("Simhash of no words reported ok")
	}
}

func TestSimhashNearDuplicates(t *testing.T) {
	base := "Brakka is a grumpy dwarf blacksmith who runs the forge at the edge of town. " +
		"She hates elves, loves good ale, and never turns away a customer with honest coin."
	a, _ := Simhash(base)
	b, _ := Simhash(base + " She has a cat.")
	c, _ := Simhash("A cheerful elven bard wanders the coast singing songs of the sea and the stars above.")
	if d := Distance(a, b); d > 10 {
		t.Errorf("near-duplicate distance %d, want <= 10", d)
	}
	if d := Distance(a, c); d < 16 {
		t.Errorf("unrelated distance %d, want >= 16", d)
	}
}

func TestLorebookSimhash(t *testing.T) {
	a, ok, err := LorebookSimhash([]byte(`{"entries": [{"content": "Hello"}, {"content": "World"}]}`))
	if err != nil || !ok {
		t.Fatalf("%v, ok=%v", err, ok)
	}
	if want, _ := Simhash("Hello\nWorld"); a != want {
		t.Fatalf("got %d, want %d", a, want)
	}
}

func TestCardData(t *testing.T) {
	data, book, err := CardData([]byte(`{"spec": "chara_card_v3", "data": {"name": "A", "extensions": {"fav": true, "world": "W", "depth": 4},
		"character_book": {"name": "B", "entries": []}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"extensions":{"depth":4},"name":"A"}` {
		t.Errorf("data = %s", data)
	}
	if string(book) != `{"name": "B", "entries": []}` {
		t.Errorf("book = %s", book)
	}
	if data, _, err := CardData([]byte(`{"spec": "chara_card_v3"}`)); data != nil || err != nil {
		t.Errorf("no data: got %s, %v", data, err)
	}
}

func TestCardDataKeepsTheFirstOfTagsThatDifferOnlyInCase(t *testing.T) {
	data, _, err := CardData([]byte(`{"spec": "chara_card_v3", "data": {"tags": ["Fantasy", "dwarf", "fantasy", 3, "FANTASY", 3]}}`))
	if err != nil || string(data) != `{"tags":["Fantasy","dwarf",3,3]}` {
		t.Fatalf("data = %s, %v", data, err)
	}
	if data, _, err := CardData([]byte(`{"spec": "chara_card_v3", "data": {"name": "A"}}`)); err != nil || string(data) != `{"name":"A"}` {
		t.Fatalf("no tags: data = %s, %v", data, err)
	}
}
