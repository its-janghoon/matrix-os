package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// This file reads a keystore passphrase.
//
// Order matters and is deliberate: an environment variable first, then a
// terminal prompt with the typing hidden. A non-interactive caller - CI, a
// script, a systemd unit - has no terminal, and a tool that only prompts is a
// tool that hangs there forever. A tool that only reads an environment variable
// puts the passphrase in every process listing on the box.

// PassphraseEnv is the environment variable checked before prompting.
const PassphraseEnv = "MATRIX_WALLET_PASSPHRASE"

// passphrasePrompt returns a function that yields the keystore passphrase,
// asking at most once and caching, so a command that touches the wallet twice
// does not ask twice.
func passphrasePrompt(out io.Writer, label string) func() (string, error) {
	var (
		cached string
		asked  bool
	)
	return func() (string, error) {
		if asked {
			return cached, nil
		}
		asked = true

		if fromEnv, ok := os.LookupEnv(PassphraseEnv); ok {
			cached = fromEnv
			return cached, nil
		}

		pass, err := readHidden(out, label, "this wallet is encrypted")
		if err != nil {
			return "", err
		}
		cached = pass
		return cached, nil
	}
}

// readHidden asks for a passphrase without echoing it. When stdin is not a
// terminal it says so and names the environment variable, rather than reading a
// line that would then be visible in a log or a shell history.
//
// `why` completes the sentence "there is no terminal to ask on", because
// "this wallet is encrypted" is the wrong reason when the passphrase is for a
// wallet that does not exist yet - and a wrong reason sends the reader looking
// in the wrong place.
func readHidden(out io.Writer, label, why string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("%s and there is no terminal to ask on; set %s", why, PassphraseEnv)
	}
	fmt.Fprintf(out, "%s: ", label)
	raw, err := term.ReadPassword(fd)
	fmt.Fprintln(out)
	if err != nil {
		return "", fmt.Errorf("failed to read the passphrase: %w", err)
	}
	return string(raw), nil
}

// newPassphrase reads a passphrase twice and requires them to match, for the
// one operation where a typo is unrecoverable: the passphrase that encrypts a
// brand-new key. Everywhere else a wrong passphrase is just a failed unlock.
func newPassphrase(out io.Writer) (string, error) {
	if fromEnv, ok := os.LookupEnv(PassphraseEnv); ok {
		if fromEnv == "" {
			return "", fmt.Errorf("%s is set but empty; an empty passphrase would not encrypt anything", PassphraseEnv)
		}
		return fromEnv, nil
	}

	first, err := readHidden(out, "Choose a passphrase for this wallet",
		"a new wallet needs a passphrase")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(first) == "" {
		return "", fmt.Errorf("an empty passphrase would not encrypt anything")
	}
	again, err := readHidden(out, "Confirm the passphrase", "a new wallet needs a passphrase")
	if err != nil {
		return "", err
	}
	if first != again {
		return "", fmt.Errorf("the passphrases do not match")
	}
	return first, nil
}

// readMnemonic reads a recovery phrase from a terminal, or from stdin when there
// is none. It is not hidden: a phrase being restored is usually read off paper,
// and hiding it makes a transcription error impossible to spot.
func readMnemonic(out io.Writer, in io.Reader) (string, error) {
	fmt.Fprint(out, "Recovery phrase: ")
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("failed to read the recovery phrase: %w", err)
	}
	return strings.Join(strings.Fields(line), " "), nil
}
