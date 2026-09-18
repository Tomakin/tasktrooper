package main

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/makifbaysal/tasktrooper/server/internal/application/webauth"
)

func TestEntryRoundTripsThroughTheServerParser(t *testing.T) {
	line, err := entry("alice", "a long enough password")
	if err != nil {
		t.Fatal(err)
	}
	users, err := webauth.ParseUsers(line)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].Name != "alice" {
		t.Fatalf("users = %+v", users)
	}
	if bcrypt.CompareHashAndPassword(users[0].Hash, []byte("a long enough password")) != nil {
		t.Fatal("hash does not verify")
	}
}

func TestEntryRejectsShortAndOverlongPasswords(t *testing.T) {
	if _, err := entry("alice", "short"); err == nil {
		t.Error("short password accepted")
	}
	if _, err := entry("alice", strings.Repeat("x", maxPasswordBytes+1)); err == nil {
		t.Error("overlong password accepted")
	}
}
