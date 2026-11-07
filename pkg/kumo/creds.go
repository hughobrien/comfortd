// SPDX-License-Identifier: GPL-3.0-or-later

package kumo

import (
	"errors"
	"os"
	"strings"
)

// ReadCredentials reads the Comfort login: email on line one, password on line two.
func ReadCredentials(path string) (user, pass string, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return "", "", errors.New("kumo: credentials file needs two lines (email, password)")
	}
	user, pass = strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
	if user == "" || pass == "" {
		return "", "", errors.New("kumo: credentials file has an empty line")
	}
	return user, pass, nil
}
