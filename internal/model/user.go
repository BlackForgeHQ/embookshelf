package model

import "time"

type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

type User struct {
	ID           string
	Email        string
	Name         string
	Role         Role
	PasswordHash string // never rendered; repo populates but handlers don't leak it.
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastSeenAt   *time.Time
}

// Display returns a friendly name, falling back to the email.
func (u User) Display() string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

// Initials returns a 1-2 letter avatar string.
func (u User) Initials() string {
	if u.Name != "" {
		parts := splitSpaces(u.Name)
		if len(parts) >= 2 {
			return string([]rune(parts[0])[0:1]) + string([]rune(parts[len(parts)-1])[0:1])
		}
		if len(parts) == 1 && len(parts[0]) > 0 {
			return string([]rune(parts[0])[0:1])
		}
	}
	if u.Email != "" {
		return string([]rune(u.Email)[0:1])
	}
	return "?"
}

func splitSpaces(s string) []string {
	var out []string
	start := -1
	for i, r := range s {
		if r == ' ' || r == '\t' {
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

type Session struct {
	ID         string
	UserID     string
	ExpiresAt  time.Time
	UserAgent  string
	CreatedAt  time.Time
	LastUsedAt time.Time
}
