package webauth

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// MinBcryptCost is the floor for a configured hash. The login endpoint faces
// the internet; a cost-4 hash from a copy-pasted example would make an offline
// guess of a leaked users file nearly free.
const MinBcryptCost = 10

const maxUsernameLen = 128

type User struct {
	Name string
	Hash []byte
}

// ParseUsers reads `name:bcrypt-hash` entries separated by commas or newlines.
// Blank entries and lines starting with # are skipped, so the same parser
// serves WEB_AUTH_USERS and a WEB_AUTH_USERS_FILE.
func ParseUsers(raw string) ([]User, error) {
	var users []User
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, entry := range strings.Split(line, ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			name, hash, ok := strings.Cut(entry, ":")
			name = strings.TrimSpace(name)
			hash = strings.TrimSpace(hash)
			if !ok || name == "" || hash == "" {
				return nil, fmt.Errorf("web auth user entry must be name:bcrypt-hash")
			}
			if len(name) > maxUsernameLen {
				return nil, fmt.Errorf("web auth user %q: name longer than %d bytes", name[:16]+"…", maxUsernameLen)
			}
			if seen[name] {
				return nil, fmt.Errorf("web auth user %q listed twice", name)
			}
			cost, err := bcrypt.Cost([]byte(hash))
			if err != nil {
				return nil, fmt.Errorf("web auth user %q: not a bcrypt hash", name)
			}
			if cost < MinBcryptCost {
				return nil, fmt.Errorf("web auth user %q: bcrypt cost %d is below %d", name, cost, MinBcryptCost)
			}
			seen[name] = true
			users = append(users, User{Name: name, Hash: []byte(hash)})
		}
	}
	return users, nil
}
