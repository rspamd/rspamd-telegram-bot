package maps

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	PatternsFile = "spam_patterns.map"
	URLsFile     = "spam_urls.map"
	UsersFile    = "spam_users.map"
)

// Manager handles reading and writing Rspamd map files.
type Manager struct {
	dir    string
	mu     sync.Mutex
	logger *slog.Logger
}

// NewManager creates a new map file manager and ensures seed files exist.
func NewManager(dir string, logger *slog.Logger) (*Manager, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create maps directory: %w", err)
	}

	m := &Manager{
		dir:    dir,
		logger: logger,
	}

	// Ensure seed files exist
	seeds := map[string]string{
		PatternsFile: "# Regex patterns for spam text (one per line)\n# Example: /crypto.*invest.*profit/i\n",
		URLsFile:     "# Spam URLs (one per line)\n",
		UsersFile:    "# Spam user IDs (one per line)\n",
	}

	for name, content := range seeds {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return nil, fmt.Errorf("create seed file %s: %w", name, err)
			}
			logger.Info("created seed map file", "file", name)
		}
	}

	return m, nil
}

// AddPattern adds a regexp pattern to the patterns map file.
func (m *Manager) AddPattern(pattern string) error {
	if err := validatePattern(pattern); err != nil {
		return err
	}
	return m.addLine(PatternsFile, pattern)
}

// RemovePattern removes a regexp pattern from the patterns map file.
func (m *Manager) RemovePattern(pattern string) error {
	return m.removeLine(PatternsFile, pattern)
}

// ListPatterns returns all patterns in the patterns map file.
func (m *Manager) ListPatterns() ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.readLines(filepath.Join(m.dir, PatternsFile))
}

// AddURL adds a URL to the URL map file.
func (m *Manager) AddURL(url string) error {
	return m.addLine(URLsFile, url)
}

// RemoveURL removes a URL from the URL map file.
func (m *Manager) RemoveURL(url string) error {
	return m.removeLine(URLsFile, url)
}

// ListURLs returns all URLs in the URL map file.
func (m *Manager) ListURLs() ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.readLines(filepath.Join(m.dir, URLsFile))
}

func (m *Manager) addLine(filename, line string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	path := filepath.Join(m.dir, filename)

	// Check for duplicates
	existing, err := m.readLines(path)
	if err != nil {
		return err
	}
	for _, l := range existing {
		if l == line {
			return fmt.Errorf("entry already exists: %s", line)
		}
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", filename, err)
	}
	defer f.Close()

	if _, err := fmt.Fprintln(f, line); err != nil {
		return fmt.Errorf("write to %s: %w", filename, err)
	}

	m.logger.Info("added map entry", "file", filename, "entry", line)
	return nil
}

func (m *Manager) removeLine(filename, line string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	path := filepath.Join(m.dir, filename)

	lines, err := m.readAllLines(path)
	if err != nil {
		return err
	}

	found := false
	var filtered []string
	for _, l := range lines {
		if strings.TrimSpace(l) == line {
			found = true
			continue
		}
		filtered = append(filtered, l)
	}

	if !found {
		return fmt.Errorf("entry not found: %s", line)
	}

	content := strings.Join(filtered, "\n")
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", filename, err)
	}

	m.logger.Info("removed map entry", "file", filename, "entry", line)
	return nil
}

// readLines returns non-empty, non-comment lines from a file.
func (m *Manager) readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}

	return lines, scanner.Err()
}

// readAllLines returns all lines from a file (including comments and empty lines).
func (m *Manager) readAllLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	return lines, scanner.Err()
}

func validatePattern(pattern string) error {
	if strings.HasPrefix(pattern, "/") {
		// Rspamd /pattern/flags format
		lastSlash := strings.LastIndex(pattern[1:], "/")
		if lastSlash < 0 {
			return fmt.Errorf("invalid pattern format: must be /pattern/flags or plain text")
		}
		re := pattern[1 : lastSlash+1]
		if _, err := regexp.Compile(re); err != nil {
			return fmt.Errorf("invalid regexp: %w", err)
		}
		return nil
	}

	// Plain text pattern
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("invalid regexp: %w", err)
	}
	return nil
}
