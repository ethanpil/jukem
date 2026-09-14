// Command apksign signs an Alpine package the way abuild does.
//
// An apk is three concatenated gzip streams: signature, control, data. nFPM
// writes the last two. This tool signs the control stream with RSA PKCS#1
// v1.5 over SHA-256 and prepends a stream that holds one tar entry named
// .SIGN.RSA256.<name> with the signature.
//
// Usage: apksign -key <private.pem> -name jukem.rsa.pub <package.apk>
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	keyPath := flag.String("key", "", "PEM private key (PKCS#1 or PKCS#8)")
	name := flag.String("name", "", "public key file name as installed in /etc/apk/keys")
	flag.Parse()
	if *keyPath == "" || *name == "" || flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: apksign -key <private.pem> -name <pubkey name> <package.apk>")
		os.Exit(2)
	}
	if err := run(*keyPath, *name, flag.Arg(0)); err != nil {
		fmt.Fprintln(os.Stderr, "apksign:", err)
		os.Exit(1)
	}
}

func run(keyPath, name, pkg string) error {
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}
	key, err := parseKey(keyPEM)
	if err != nil {
		return fmt.Errorf("parse key: %w", err)
	}
	data, err := os.ReadFile(pkg)
	if err != nil {
		return err
	}
	signed, err := Sign(data, key, name)
	if err != nil {
		return err
	}
	tmp := pkg + ".signed"
	if err := os.WriteFile(tmp, signed, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, pkg)
}
