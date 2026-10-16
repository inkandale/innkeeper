package library

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"testing"
)

// grayPNG encodes a 16×16 gray image whose pixel (0,0) is first.
func grayPNG(t *testing.T, first uint8) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 16, 16))
	for i := range img.Pix {
		img.Pix[i] = uint8(i)
	}
	img.Pix[0] = first
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withText inserts a tEXt chunk before IEND, as SillyTavern embeds a card.
func withText(b []byte, text string) []byte {
	data := append([]byte("chara\x00"), text...)
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	body := append([]byte("tEXt"), data...)
	chunk = append(chunk, body...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(body))
	iend := len(b) - 12
	return append(append(append([]byte{}, b[:iend]...), chunk...), b[iend:]...)
}

func TestSameImage(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	put := func(b []byte) string {
		t.Helper()
		blob, err := lib.PutBlob(ctx, bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		return blob.SHA256
	}
	plain := put(grayPNG(t, 0))
	tagged := put(withText(grayPNG(t, 0), "card json"))
	changed := put(grayPNG(t, 1))
	if plain == tagged {
		t.Fatal("the tEXt chunk didn't change the bytes")
	}
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{plain, plain, true},
		{plain, tagged, true},
		{plain, changed, false},
		{plain, "", false},
		{"", "", true},
	} {
		got, err := lib.SameImage(ctx, c.a, c.b)
		if err != nil || got != c.want {
			t.Errorf("SameImage(%.8s, %.8s) = %v, %v; want %v", c.a, c.b, got, err, c.want)
		}
	}
}

func TestSameImageFillsOldBlobs(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, err := lib.PutBlob(ctx, bytes.NewReader(grayPNG(t, 0)))
	if err != nil {
		t.Fatal(err)
	}
	b, err := lib.PutBlob(ctx, bytes.NewReader(withText(grayPNG(t, 0), "x")))
	if err != nil {
		t.Fatal(err)
	}
	// A blob stored before the column existed has no pixel_hash.
	if _, err := lib.pool.Exec(ctx, `UPDATE blob SET pixel_hash = NULL`); err != nil {
		t.Fatal(err)
	}
	if same, err := lib.SameImage(ctx, a.SHA256, b.SHA256); err != nil || !same {
		t.Fatalf("SameImage = %v, %v", same, err)
	}
	var n int
	if err := lib.pool.QueryRow(ctx, `SELECT count(*) FROM blob WHERE pixel_hash IS NOT NULL`).Scan(&n); err != nil || n != 2 {
		t.Errorf("%d blobs got a pixel_hash, %v", n, err)
	}
}
