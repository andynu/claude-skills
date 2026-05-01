package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type config struct {
	SourceDir string `json:"source_dir"`
}

// configPath returns the standard location for the config file. Uses
// os.UserConfigDir so it picks the right place per-platform:
//   - Linux:   $XDG_CONFIG_HOME/claude-skills/config.json (or ~/.config/...)
//   - macOS:   ~/Library/Application Support/claude-skills/config.json
//   - Windows: %AppData%\claude-skills\config.json
func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "claude-skills", "config.json"), nil
}

// loadConfig reads the config file. Returns (nil, path, nil) when the file
// doesn't exist so the caller can decide whether to onboard.
func loadConfig() (*config, string, error) {
	path, err := configPath()
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, path, nil
		}
		return nil, path, err
	}
	var c config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, path, fmt.Errorf("parse %s: %w", path, err)
	}
	return &c, path, nil
}

func saveConfig(path string, c *config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func expandHome(p string) string {
	if p == "" || !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// runOnboarding interactively asks for the source library directory and
// writes the config file. It uses plain stdin/stdout so it can run before
// the bubbletea program takes over the screen.
func runOnboarding(out io.Writer, in io.Reader, configFile string) (*config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	defaultDir := filepath.Join(home, "claude-skills")

	fmt.Fprintln(out, "Welcome to claude-skills.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "This tool links skill directories from a source library into")
	fmt.Fprintln(out, "your project or user .claude/skills directory.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Let's set up your config. (Press Enter to accept defaults.)")
	fmt.Fprintln(out)

	reader := bufio.NewReader(in)

	fmt.Fprintf(out, "Source library directory [%s]: ", defaultDir)
	answer, err := readLine(reader)
	if err != nil {
		return nil, err
	}
	if answer == "" {
		answer = defaultDir
	}
	src, err := filepath.Abs(expandHome(answer))
	if err != nil {
		return nil, err
	}

	info, statErr := os.Stat(src)
	switch {
	case statErr == nil && info.IsDir():
		// already there, nothing to do
	case statErr == nil && !info.IsDir():
		return nil, fmt.Errorf("%s exists but is not a directory", src)
	case os.IsNotExist(statErr):
		fmt.Fprintf(out, "%s does not exist. Create it? [Y/n]: ", src)
		ans, err := readLine(reader)
		if err != nil {
			return nil, err
		}
		ans = strings.ToLower(ans)
		if ans != "" && ans != "y" && ans != "yes" {
			return nil, fmt.Errorf("aborted: source directory must exist")
		}
		if err := os.MkdirAll(src, 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", src, err)
		}
		fmt.Fprintf(out, "Created %s\n", src)
	default:
		return nil, statErr
	}

	cfg := &config{SourceDir: src}
	if err := saveConfig(configFile, cfg); err != nil {
		return nil, fmt.Errorf("write config: %w", err)
	}
	fmt.Fprintf(out, "Saved config to %s\n", configFile)
	fmt.Fprintln(out)
	return cfg, nil
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
