// Command web-passwd prints a WEB_AUTH_USERS entry for one user:
//
//	go run ./cmd/web-passwd alice >> ~/.config/tasktrooper/web-users
//
// The password is read from the terminal without echo (asked twice), or from
// the first line of stdin when stdin is not a terminal. It is never taken from
// argv, where every process on the machine could read it.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
)

const (
	cost = 12
	// bcrypt only looks at the first 72 bytes; accepting more would let two
	// different passwords share a hash without anyone noticing.
	maxPasswordBytes = 72
	minPasswordRunes = 12
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "web-passwd:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin *os.File, stdout, stderr io.Writer) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return errors.New("usage: web-passwd <username>")
	}
	user := strings.TrimSpace(args[0])
	if strings.ContainsAny(user, ":,\n#") {
		return errors.New("username may not contain ':', ',', '#' or a newline")
	}

	var password string
	if term.IsTerminal(int(stdin.Fd())) {
		first, err := prompt(stdin, stderr, "Password: ")
		if err != nil {
			return err
		}
		second, err := prompt(stdin, stderr, "Again: ")
		if err != nil {
			return err
		}
		if first != second {
			return errors.New("the passwords do not match")
		}
		password = first
	} else {
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		password = strings.TrimRight(line, "\r\n")
	}

	line, err := entry(user, password)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, line)
	return err
}

func prompt(stdin *os.File, stderr io.Writer, label string) (string, error) {
	fmt.Fprint(stderr, label)
	b, err := term.ReadPassword(int(stdin.Fd()))
	fmt.Fprintln(stderr)
	return string(b), err
}

func entry(user, password string) (string, error) {
	if len([]rune(password)) < minPasswordRunes {
		return "", fmt.Errorf("the password must be at least %d characters", minPasswordRunes)
	}
	if len(password) > maxPasswordBytes {
		return "", fmt.Errorf("the password must be at most %d bytes", maxPasswordBytes)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return user + ":" + string(hash), nil
}
