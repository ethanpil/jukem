package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"jukem/internal/api"
	"jukem/internal/config"
	"jukem/internal/store"
)

// runResetPassword sets a new login password from the console and signs
// out every session. It touches nothing else.
func runResetPassword(args []string) error {
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	cfgPath := fs.String("config", "/etc/jukem/config.yaml", "bootstrap config file")
	fromStdin := fs.Bool("password-stdin", false, "read the password from stdin instead of the terminal")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, _, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	password, err := readPassword(*fromStdin)
	if err != nil {
		return err
	}
	if len(password) < 8 {
		return errors.New("the password must have at least 8 characters")
	}
	hash, err := api.HashPassword(password)
	if err != nil {
		return err
	}
	db, err := store.Open(filepath.Join(cfg.DataDir, "jukem.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx, filepath.Join(cfg.DataDir, "snapshots")); err != nil {
		return err
	}
	if err := db.SetPasswordHash(ctx, hash); err != nil {
		return err
	}
	fmt.Println("Password set. Every web session is signed out.")
	return nil
}

func readPassword(fromStdin bool) (string, error) {
	if fromStdin || !term.IsTerminal(int(os.Stdin.Fd())) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Print("New password: ")
	first, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	fmt.Print("Again: ")
	second, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", errors.New("the passwords differ")
	}
	return string(first), nil
}
