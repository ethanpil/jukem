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
	"errors"
	"fmt"
	"io"
	"time"
)

// parseKey reads a PKCS#1 ("RSA PRIVATE KEY") or PKCS#8 ("PRIVATE KEY") PEM.
func parseKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("not an RSA key")
		}
		return rk, nil
	}
	return nil, fmt.Errorf("unsupported PEM type %q", block.Type)
}

// SplitStreams splits a concatenated gzip file into its member streams and
// returns the raw bytes of each. bytes.Reader implements io.ByteReader, so
// gzip reads exactly one member without reading ahead and the offset after
// each member is exact.
func SplitStreams(data []byte) ([][]byte, error) {
	var streams [][]byte
	r := bytes.NewReader(data)
	for r.Len() > 0 {
		start := int(r.Size()) - r.Len()
		zr, err := gzip.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("stream at %d: %w", start, err)
		}
		zr.Multistream(false)
		if _, err := io.Copy(io.Discard, zr); err != nil {
			return nil, fmt.Errorf("stream at %d: %w", start, err)
		}
		end := int(r.Size()) - r.Len()
		streams = append(streams, data[start:end])
	}
	return streams, nil
}

// Sign takes an unsigned two-stream apk and returns the signed three-stream
// apk. name is the public key file name apk looks for in /etc/apk/keys.
func Sign(pkg []byte, key *rsa.PrivateKey, name string) ([]byte, error) {
	streams, err := SplitStreams(pkg)
	if err != nil {
		return nil, err
	}
	if len(streams) != 2 {
		return nil, fmt.Errorf("expected an unsigned package with 2 gzip streams, found %d", len(streams))
	}
	control, data := streams[0], streams[1]

	digest := sha256.Sum256(control)
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return nil, err
	}
	sigStream, err := signatureStream(".SIGN.RSA256."+name, sig)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(sigStream)+len(control)+len(data))
	out = append(out, sigStream...)
	out = append(out, control...)
	out = append(out, data...)
	return out, nil
}

// signatureStream builds the gzip stream that holds the signature entry. As
// with abuild-tar --cut, the tar end-of-archive marker is omitted: the writer
// is flushed but not closed.
func signatureStream(entryName string, sig []byte) ([]byte, error) {
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	hdr := &tar.Header{
		Name:     entryName,
		Mode:     0o644,
		Size:     int64(len(sig)),
		ModTime:  time.Now().Truncate(time.Second),
		Typeflag: tar.TypeReg,
		Format:   tar.FormatUSTAR,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return nil, err
	}
	if _, err := tw.Write(sig); err != nil {
		return nil, err
	}
	if err := tw.Flush(); err != nil {
		return nil, err
	}
	var gz bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	if _, err := zw.Write(tarBuf.Bytes()); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return gz.Bytes(), nil
}
