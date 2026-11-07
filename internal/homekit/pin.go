// SPDX-License-Identifier: GPL-3.0-or-later

package homekit

import (
	"crypto/rand"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/brutella/hap"
)

const pinFile = "pin.txt"

// LoadOrGeneratePin returns the persisted 8-digit HomeKit PIN from
// stateDir/pin.txt, generating and saving a strong one if absent or invalid.
func LoadOrGeneratePin(stateDir string) (string, error) {
	path := filepath.Join(stateDir, pinFile)
	if data, err := os.ReadFile(path); err == nil {
		if pin := strings.TrimSpace(string(data)); validPin(pin) {
			return pin, nil
		}
	}
	pin, err := generatePin()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(pin+"\n"), 0o600); err != nil {
		return "", err
	}
	return pin, nil
}

func validPin(pin string) bool {
	if len(pin) != 8 {
		return false
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			return false
		}
	}
	return !hap.InvalidPins[pin]
}

func generatePin() (string, error) {
	for {
		var sb strings.Builder
		for range 8 {
			n, err := rand.Int(rand.Reader, big.NewInt(10))
			if err != nil {
				return "", err
			}
			sb.WriteByte(byte('0' + n.Int64()))
		}
		if pin := sb.String(); !hap.InvalidPins[pin] {
			return pin, nil
		}
	}
}

// FormatPin renders an 8-digit pin in Home's "123-45-678" layout.
func FormatPin(pin string) string {
	if len(pin) != 8 {
		return pin
	}
	return pin[:3] + "-" + pin[3:5] + "-" + pin[5:]
}
