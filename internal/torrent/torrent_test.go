package torrent

import (
	"crypto/sha1" //nolint:gosec // test vectors for v1 info-hashes
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

const info = "d6:lengthi12e4:name8:game.gba12:piece lengthi16384e6:pieces20:aaaaaaaaaaaaaaaaaaaae"

func TestID(t *testing.T) {
	file := []byte("d8:announce14:http://tracker4:info" + info + "e")
	got, err := ID(file)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum([]byte(info)) //nolint:gosec // see import
	if want := hex.EncodeToString(sum[:]); got != want {
		t.Errorf("ID = %s, want %s", got, want)
	}

	v2 := "d12:meta versioni2e4:name8:game.gba12:piece lengthi16384ee"
	got, err = ID([]byte("d4:info" + v2 + "e"))
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256([]byte(v2))
	if want := hex.EncodeToString(s[:20]); got != want {
		t.Errorf("v2 ID = %s, want %s", got, want)
	}
}

func TestIDRejectsGarbage(t *testing.T) {
	for _, b := range []string{"", "le", "d4:infoi1ee", "d4:info", "d3:fooi1ee", "d9999999999:x", "d4:infod" + "e"[:0]} {
		if _, err := ID([]byte(b)); !errors.Is(err, ErrInvalid) {
			t.Errorf("ID(%q) err = %v", b, err)
		}
	}
}

func TestMagnetID(t *testing.T) {
	hexHash := "c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	raw, _ := hex.DecodeString(hexHash)
	b32 := base32.StdEncoding.EncodeToString(raw)
	for _, m := range []string{
		"magnet:?xt=urn:btih:" + hexHash + "&dn=game",
		"magnet:?xt=urn:btih:C12FE1C06BBA254A9DC9F519B335AA7C1367A88A",
		"magnet:?xt=urn:btih:" + b32,
	} {
		if got, err := MagnetID(m); err != nil || got != hexHash {
			t.Errorf("MagnetID(%q) = %q, %v", m, got, err)
		}
	}
	v2 := "1220" + hexHash + hexHash[:24]
	if got, err := MagnetID("magnet:?xt=urn:btmh:" + v2); err != nil || got != hexHash {
		t.Errorf("btmh = %q, %v", got, err)
	}
	for _, m := range []string{"http://example.com", "magnet:?dn=x", "magnet:?xt=urn:btih:zz"} {
		if _, err := MagnetID(m); !errors.Is(err, ErrInvalid) {
			t.Errorf("MagnetID(%q) err = %v", m, err)
		}
	}
}

func TestResolve(t *testing.T) {
	file := "d4:info" + info + "e"
	magnet := "magnet:?xt=urn:btih:c12fe1c06bba254a9dc9f519b335aa7c1367a88a"
	mux := http.NewServeMux()
	mux.HandleFunc("/file", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(file)) })
	mux.HandleFunc("/hop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/file", http.StatusFound) })
	mux.HandleFunc("/magnet", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", magnet)
		w.WriteHeader(http.StatusMovedPermanently)
	})
	mux.HandleFunc("/ftp", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "ftp://elsewhere/x")
		w.WriteHeader(http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx := t.Context()

	s, err := Resolve(ctx, srv.Client(), "", srv.URL+"/hop")
	if err != nil || string(s.File) != file || s.Hash == "" {
		t.Fatalf("file via redirect: %+v %v", s, err)
	}
	s, err = Resolve(ctx, srv.Client(), "", srv.URL+"/magnet")
	if err != nil || s.Magnet != magnet || s.Hash != "c12fe1c06bba254a9dc9f519b335aa7c1367a88a" {
		t.Fatalf("redirect to magnet: %+v %v", s, err)
	}
	if s, err := Resolve(ctx, srv.Client(), magnet, srv.URL+"/file"); err != nil || s.Magnet != magnet {
		t.Fatalf("magnet preferred: %+v %v", s, err)
	}
	if _, err := Resolve(ctx, srv.Client(), "", srv.URL+"/ftp"); !errors.Is(err, ErrInvalid) {
		t.Errorf("ftp redirect: %v", err)
	}
	if _, err := Resolve(ctx, srv.Client(), "", srv.URL+"/missing"); err == nil {
		t.Error("404 accepted")
	}
}
