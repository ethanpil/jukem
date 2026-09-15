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
// out every session. It does not change other data.
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
	dbPath := filepath.Join(cfg.DataDir, "jukem.db")
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("no database at %s: start the service once, then run reset-password", dbPath)
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
	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer giveBackOwnership(cfg.DataDir, dbPath)
	defer db.Close()
	ctx := context.Background()
	ms, err := store.Migrations()
	if err != nil {
		return err
	}
	// The service owns upgrades. A database at another version needs a
	// service start, not a password reset.
	if v, err := db.Version(ctx); err != nil {
		return err
	} else if v != len(ms) {
		return fmt.Errorf("database schema version %d differs from this binary (%d): start the service first", v, len(ms))
	}
	if err := db.SetPasswordHash(ctx, hash); err != nil {
		return err
	}
	fmt.Println("Password set. Every web session is signed out.")
	return nil
}

// giveBackOwnership hands the database files to the owner of the data
// directory. A root console creates the WAL and SHM files as root, and the
// service user could not write them afterwards.
func giveBackOwnership(dataDir, dbPath string) {
	if os.Getuid() != 0 {
		return
	}
	uid, gid, ok := ownerOf(dataDir)
	if !ok {
		return
	}
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if _, err := os.Stat(p); err == nil {
			os.Chown(p, uid, gid)
		}
	}
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
