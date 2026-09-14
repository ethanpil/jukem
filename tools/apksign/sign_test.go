package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"io"
	"testing"
)

// gzTar returns a gzip stream holding one tar entry, without the end marker
// when cut is set, the way abuild-tar --cut writes control streams.
func gzTar(t *testing.T, name string, body []byte, cut bool) []byte {
	t.Helper()
	var tb bytes.Buffer
	tw := tar.NewWriter(&tb)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	tw.Write(body)
	if cut {
		tw.Flush()
	} else {
		tw.Close()
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(tb.Bytes())
	zw.Close()
	return gz.Bytes()
}

func TestSignProducesVerifiableThreeStreamPackage(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	control := gzTar(t, ".PKGINFO", []byte("pkgname = jukem\npkgver = 1.0.0-r0\n"), true)
	data := gzTar(t, "usr/bin/jukem", bytes.Repeat([]byte("x"), 5000), false)
	pkg := append(append([]byte{}, control...), data...)

	signed, err := Sign(pkg, key, "jukem.rsa.pub")
	if err != nil {
		t.Fatal(err)
	}
	streams, err := SplitStreams(signed)
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 3 {
		t.Fatalf("expected 3 streams, got %d", len(streams))
	}
	if !bytes.Equal(streams[1], control) || !bytes.Equal(streams[2], data) {
		t.Fatal("control or data stream changed")
	}

	zr, err := gzip.NewReader(bytes.NewReader(streams[0]))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Name != ".SIGN.RSA256.jukem.rsa.pub" {
		t.Fatalf("entry name %q", hdr.Name)
	}
	sig, err := io.ReadAll(tr)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(control)
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
	if _, err := tr.Next(); err != io.EOF && err != io.ErrUnexpectedEOF {
		t.Fatalf("expected the cut tar to end, got %v", err)
	}
}

func TestSignRejectsAlreadySigned(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 1024)
	a := gzTar(t, "a", []byte("a"), true)
	pkg := append(append(append([]byte{}, a...), a...), a...)
	if _, err := Sign(pkg, key, "k"); err == nil {
		t.Fatal("expected error for 3 streams")
	}
}

func TestParseKeyFormats(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 1024)
	p1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if _, err := parseKey(p1); err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	p8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if _, err := parseKey(p8); err != nil {
		t.Fatal(err)
	}
	if _, err := parseKey([]byte("nope")); err == nil {
		t.Fatal("expected error")
	}
}
