// claude-skills: pick which skills from ~/cloud/claude-skills are
// linked into <cwd>/.claude/skills as symlinks (or junctions on Windows).
//
// State lives in the filesystem: a skill is "enabled" iff a symlink/junction
// at <cwd>/.claude/skills/<name> points into the source directory. The TUI
// reads that state, lets you toggle, and on save diffs against it — adding
// or removing links to match.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	colAccent  = lipgloss.Color("#7D56F4")
	colDim     = lipgloss.Color("241")
	colSubtle  = lipgloss.Color("245")
	colText    = lipgloss.Color("252")
	colOn      = lipgloss.Color("#A3BE8C")
	colChange  = lipgloss.Color("#EBCB8B")
	colMissing = lipgloss.Color("#BF616A")
	colProject = lipgloss.Color("#88C0D0")
	colUser    = lipgloss.Color("#D08770")

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colAccent).
			Padding(0, 1)

	labelStyle = lipgloss.NewStyle().Foreground(colDim)
	pathStyle  = lipgloss.NewStyle().Foreground(colText)

	scopeBadge = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#1D2021")).
			Padding(0, 1)

	cursorStyle      = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	checkedStyle     = lipgloss.NewStyle().Foreground(colOn).Bold(true)
	inheritedStyle   = lipgloss.NewStyle().Foreground(colSubtle)
	uncheckedStyle   = lipgloss.NewStyle().Foreground(colDim)
	changedStyle     = lipgloss.NewStyle().Foreground(colChange).Bold(true)
	nameOnStyle      = lipgloss.NewStyle().Foreground(colText)
	nameOffStyle     = lipgloss.NewStyle().Foreground(colSubtle)
	cursorRowStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
	missingStyle     = lipgloss.NewStyle().Foreground(colMissing).Italic(true)
	projectTagStyle  = lipgloss.NewStyle().Foreground(colProject).Bold(true)
	userTagStyle     = lipgloss.NewStyle().Foreground(colUser).Bold(true)
	helpStyle        = lipgloss.NewStyle().Foreground(colDim)
	helpKeyStyle     = lipgloss.NewStyle().Foreground(colText).Bold(true)
	dirtyCountStyle  = lipgloss.NewStyle().Foreground(colChange).Bold(true)
	emptyStyle       = lipgloss.NewStyle().Foreground(colDim).Italic(true)
	headerRuleStyle  = lipgloss.NewStyle().Foreground(colDim)
	scrollHintStyle  = lipgloss.NewStyle().Foreground(colDim).Italic(true)
)

type skill struct {
	name       string
	sourcePath string // empty if source is missing (orphan link)
	linked     bool   // currently linked into the chosen target
	chosen     bool   // user wants it linked after save
	inOther    bool   // also linked in the OTHER scope (e.g., already global)
}

func (s skill) changed() bool { return s.linked != s.chosen }
func (s skill) missing() bool { return s.sourcePath == "" }

type model struct {
	sourceDir string
	targetDir string
	scope     string // "project" or "user", for display
	skills    []skill
	cursor    int
	offset    int // first visible row in the viewport
	width     int
	height    int
	saved     bool
	err       error
	quitMsg   string
}

// sourceRoot resolves the skill library location. Precedence:
//  1. CLAUDE_SKILLS_DIR env var (escape hatch, useful for testing)
//  2. source_dir from the config file
//  3. interactive onboarding wizard, which writes the config file
func sourceRoot() (string, error) {
	if env := os.Getenv("CLAUDE_SKILLS_DIR"); env != "" {
		return env, nil
	}
	cfg, path, err := loadConfig()
	if err != nil {
		return "", err
	}
	if cfg == nil {
		cfg, err = runOnboarding(os.Stdout, os.Stdin, path)
		if err != nil {
			return "", err
		}
	}
	if cfg.SourceDir == "" {
		return "", fmt.Errorf("source_dir is empty in %s", path)
	}
	return cfg.SourceDir, nil
}

// claudeDirStatus reports whether <base>/.claude exists and is a directory.
// Symlinks are followed so a symlinked .claude dir counts as a directory.
type dirStatus struct {
	exists bool
	isDir  bool
}

func claudeDirStatus(path string) (dirStatus, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return dirStatus{}, nil
		}
		return dirStatus{}, err
	}
	return dirStatus{exists: true, isDir: info.IsDir()}, nil
}

// chooseTarget picks where .claude/skills should live. scope is one of
// "auto", "project", or "user". In auto mode it prefers the location
// closest to pwd: project if <cwd>/.claude exists, else user.
func chooseTarget(cwd, home, scope string) (target, scopeName string, err error) {
	projectClaude := filepath.Join(cwd, ".claude")
	userClaude := filepath.Join(home, ".claude")

	pst, perr := claudeDirStatus(projectClaude)
	if perr != nil {
		return "", "", fmt.Errorf("%s: %w", projectClaude, perr)
	}
	if pst.exists && !pst.isDir {
		return "", "", fmt.Errorf("%s exists but is not a directory", projectClaude)
	}
	ust, uerr := claudeDirStatus(userClaude)
	if uerr != nil {
		return "", "", fmt.Errorf("%s: %w", userClaude, uerr)
	}
	if ust.exists && !ust.isDir {
		return "", "", fmt.Errorf("%s exists but is not a directory", userClaude)
	}

	switch scope {
	case "project":
		if !pst.exists {
			return "", "", fmt.Errorf("%s does not exist; create it first or use -scope=user", projectClaude)
		}
		return filepath.Join(projectClaude, "skills"), "project", nil
	case "user":
		if !ust.exists {
			return "", "", fmt.Errorf("%s does not exist; create it first", userClaude)
		}
		return filepath.Join(userClaude, "skills"), "user", nil
	case "", "auto":
		if pst.exists {
			return filepath.Join(projectClaude, "skills"), "project", nil
		}
		if ust.exists {
			return filepath.Join(userClaude, "skills"), "user", nil
		}
		return "", "", fmt.Errorf("no .claude directory found at %s or %s; create one or pass -scope", projectClaude, userClaude)
	default:
		return "", "", fmt.Errorf("invalid -scope %q (want auto, project, or user)", scope)
	}
}

func loadModel(scope string) (model, error) {
	src, err := sourceRoot()
	if err != nil {
		return model{}, err
	}
	if _, err := os.Stat(src); err != nil {
		return model{}, fmt.Errorf("source dir %s: %w", src, err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return model{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return model{}, err
	}
	target, scopeName, err := chooseTarget(cwd, home, scope)
	if err != nil {
		return model{}, err
	}

	byName := map[string]*skill{}

	// Skills available from source.
	entries, err := os.ReadDir(src)
	if err != nil {
		return model{}, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
			continue
		}
		byName[name] = &skill{name: name, sourcePath: filepath.Join(src, name)}
	}

	// Existing links in target — including orphans whose source was removed.
	scanLinks(target, src, func(name string) {
		s, found := byName[name]
		if !found {
			s = &skill{name: name} // orphan: source missing
			byName[name] = s
		}
		s.linked = true
		s.chosen = true
	})

	// Also scan the OTHER scope so we can flag skills already enabled there
	// (e.g., when editing project, note which skills are already global).
	if other := otherSkillsDir(cwd, home, scopeName); other != "" {
		scanLinks(other, src, func(name string) {
			if s, ok := byName[name]; ok {
				s.inOther = true
			}
		})
	}

	skills := make([]skill, 0, len(byName))
	for _, s := range byName {
		skills = append(skills, *s)
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].name < skills[j].name })

	return model{sourceDir: src, targetDir: target, scope: scopeName, skills: skills}, nil
}

func (m model) Init() tea.Cmd { return nil }

// visibleRows returns how many skill rows fit in the current viewport.
// Header and footer combined are reserved.
func (m model) visibleRows() int {
	const reserved = 7 // title + 2 path lines + blank + footer (2 lines) + 1 slack
	rows := m.height - reserved
	if rows < 3 {
		rows = 3
	}
	return rows
}

func (m model) clampOffset() model {
	rows := m.visibleRows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
	max := len(m.skills) - rows
	if max < 0 {
		max = 0
	}
	if m.offset > max {
		m.offset = max
	}
	return m
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m.clampOffset(), nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.quitMsg = "Cancelled, no changes."
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.skills)-1 {
				m.cursor++
			}
		case "pgup":
			m.cursor -= m.visibleRows()
			if m.cursor < 0 {
				m.cursor = 0
			}
		case "pgdown":
			m.cursor += m.visibleRows()
			if m.cursor > len(m.skills)-1 {
				m.cursor = len(m.skills) - 1
			}
		case "g", "home":
			m.cursor = 0
		case "G", "end":
			if len(m.skills) > 0 {
				m.cursor = len(m.skills) - 1
			}
		case " ", "x":
			if len(m.skills) == 0 {
				break
			}
			s := &m.skills[m.cursor]
			// Orphans (source missing) can be toggled OFF to clean up the
			// stale link, but can't be turned back ON.
			if s.missing() && !s.chosen {
				break
			}
			s.chosen = !s.chosen
		case "enter":
			if err := apply(m.targetDir, m.skills); err != nil {
				m.err = err
			} else {
				m.saved = true
			}
			return m, tea.Quit
		}
		return m.clampOffset(), nil
	}
	return m, nil
}

func (m model) View() string {
	var b strings.Builder

	// Header.
	scope := scopeBadge.Background(colProject).Render("project")
	if m.scope == "user" {
		scope = scopeBadge.Background(colUser).Render("user")
	}
	b.WriteString(titleStyle.Render("Claude skills"))
	b.WriteString("  ")
	b.WriteString(scope)
	b.WriteByte('\n')
	b.WriteString(labelStyle.Render("  source: "))
	b.WriteString(pathStyle.Render(m.sourceDir))
	b.WriteByte('\n')
	b.WriteString(labelStyle.Render("  target: "))
	b.WriteString(pathStyle.Render(m.targetDir))
	b.WriteString("\n\n")

	if len(m.skills) == 0 {
		b.WriteString(emptyStyle.Render("No skills found. Add a directory under the source dir to get started."))
		b.WriteByte('\n')
	}

	// Total dirty count across all skills (not just the visible window).
	dirty := 0
	for _, s := range m.skills {
		if s.changed() {
			dirty++
		}
	}

	// Visible window.
	rows := m.visibleRows()
	end := m.offset + rows
	if end > len(m.skills) {
		end = len(m.skills)
	}
	for i := m.offset; i < end; i++ {
		b.WriteString(renderRow(m.skills[i], i == m.cursor, m.scope))
		b.WriteByte('\n')
	}
	// Scroll hint if there's more above/below.
	if len(m.skills) > rows {
		hint := fmt.Sprintf("  -- %d-%d of %d --", m.offset+1, end, len(m.skills))
		b.WriteString(scrollHintStyle.Render(hint))
		b.WriteByte('\n')
	}

	// Footer.
	b.WriteByte('\n')
	if dirty > 0 {
		b.WriteString(dirtyCountStyle.Render(fmt.Sprintf("● %d pending", dirty)))
		b.WriteString("   ")
	}
	b.WriteString(helpRow())
	b.WriteByte('\n')
	return b.String()
}

func renderRow(s skill, selected bool, scope string) string {
	cursor := "  "
	if selected {
		cursor = cursorStyle.Render("▸ ")
	}
	// Filled circle = will be active after save. Green if linked locally,
	// gray if only inherited from the other scope. Empty circle = inactive.
	var box string
	switch {
	case s.chosen:
		box = checkedStyle.Render("●")
	case s.inOther:
		box = inheritedStyle.Render("●")
	default:
		box = uncheckedStyle.Render("○")
	}
	marker := " "
	if s.changed() {
		marker = changedStyle.Render("*")
	}
	name := nameOffStyle.Render(s.name)
	if s.chosen {
		name = nameOnStyle.Render(s.name)
	}
	if selected {
		name = cursorRowStyle.Render(s.name)
	}
	var suffix string
	inProject := (scope == "project" && s.chosen) || (scope == "user" && s.inOther)
	inUser := (scope == "user" && s.chosen) || (scope == "project" && s.inOther)
	if inProject {
		suffix += "  " + projectTagStyle.Render("[project]")
	}
	if inUser {
		suffix += "  " + userTagStyle.Render("[user]")
	}
	if s.missing() {
		suffix += "  " + missingStyle.Render("(source missing)")
	}
	return fmt.Sprintf("%s%s %s %s%s", cursor, box, marker, name, suffix)
}

func helpRow() string {
	keys := []struct{ k, desc string }{
		{"space", "toggle"},
		{"enter", "apply"},
		{"j/k", "move"},
		{"g/G", "top/bottom"},
		{"q", "cancel"},
	}
	parts := make([]string, 0, len(keys))
	for _, kv := range keys {
		parts = append(parts, helpKeyStyle.Render(kv.k)+helpStyle.Render(" "+kv.desc))
	}
	return helpStyle.Render("  ") + strings.Join(parts, helpStyle.Render("  ·  "))
}

func apply(targetDir string, skills []skill) error {
	// Auto-create .claude/skills, but only if the .claude parent already exists.
	parent := filepath.Dir(targetDir)
	if info, err := os.Stat(parent); err != nil {
		return fmt.Errorf("%s: %w", parent, err)
	} else if !info.IsDir() {
		return fmt.Errorf("%s exists but is not a directory", parent)
	}
	if info, err := os.Stat(targetDir); err == nil && !info.IsDir() {
		return fmt.Errorf("%s exists but is not a directory", targetDir)
	}
	if err := os.Mkdir(targetDir, 0o755); err != nil && !os.IsExist(err) {
		return err
	}
	var errs []error
	for _, s := range skills {
		path := filepath.Join(targetDir, s.name)
		switch {
		case s.chosen && !s.linked:
			if s.missing() {
				continue // can't link a missing source
			}
			if _, err := os.Lstat(path); err == nil {
				errs = append(errs, fmt.Errorf("%s: already exists in target, refusing to overwrite", s.name))
				continue
			}
			if err := linkDir(s.sourcePath, path); err != nil {
				errs = append(errs, fmt.Errorf("link %s: %w", s.name, err))
			}
		case !s.chosen && s.linked:
			// We only ever remove links we identified as ours (verified during load).
			if err := os.Remove(path); err != nil {
				errs = append(errs, fmt.Errorf("unlink %s: %w", s.name, err))
			}
		}
	}
	return errors.Join(errs...)
}

// otherSkillsDir returns the .claude/skills path for the scope that *isn't*
// chosen, when its .claude exists. Empty string if the other scope has no
// .claude or if cwd/home resolve to the same place.
func otherSkillsDir(cwd, home, scope string) string {
	switch scope {
	case "project":
		st, _ := claudeDirStatus(filepath.Join(home, ".claude"))
		if st.exists && st.isDir {
			out := filepath.Join(home, ".claude", "skills")
			// Avoid double-counting if cwd == home.
			if a, _ := filepath.Abs(out); a == filepath.Join(cwd, ".claude", "skills") {
				return ""
			}
			return out
		}
	case "user":
		st, _ := claudeDirStatus(filepath.Join(cwd, ".claude"))
		if st.exists && st.isDir {
			out := filepath.Join(cwd, ".claude", "skills")
			if a, _ := filepath.Abs(out); a == filepath.Join(home, ".claude", "skills") {
				return ""
			}
			return out
		}
	}
	return ""
}

// scanLinks calls visit for each entry in dir that is a symlink/junction
// pointing into srcRoot. Silently no-ops if dir is unreadable.
func scanLinks(dir, srcRoot string, visit func(name string)) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	absSrc, _ := filepath.Abs(srcRoot)
	for _, e := range entries {
		dest, ok := readLinkTarget(filepath.Join(dir, e.Name()))
		if !ok {
			continue
		}
		absDest, _ := filepath.Abs(dest)
		if !strings.HasPrefix(absDest, absSrc) {
			continue
		}
		visit(e.Name())
	}
}

// readLinkTarget returns the resolved link destination if path is a symlink
// or a Windows junction, and false otherwise.
func readLinkTarget(path string) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", false
	}
	// Symlinks on all platforms; junctions on Windows surface as ModeSymlink
	// in Go 1.23+. Older behavior used ModeIrregular — accept both.
	if info.Mode()&(os.ModeSymlink|os.ModeIrregular) == 0 {
		return "", false
	}
	dest, err := os.Readlink(path)
	if err != nil {
		return "", false
	}
	return dest, true
}

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "0.1.0"

func main() {
	listOnly := flag.Bool("list", false, "print enabled/available skills and exit")
	scope := flag.String("scope", "auto", "where to link skills: auto (closest to pwd), project (<cwd>/.claude), or user (~/.claude)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	m, err := loadModel(*scope)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if *listOnly {
		for _, s := range m.skills {
			state := "off"
			if s.linked {
				state = "on"
			}
			extras := ""
			inProject := (m.scope == "project" && s.linked) || (m.scope == "user" && s.inOther)
			inUser := (m.scope == "user" && s.linked) || (m.scope == "project" && s.inOther)
			if inProject {
				extras += " [project]"
			}
			if inUser {
				extras += " [user]"
			}
			if s.missing() {
				extras += " (source missing)"
			}
			fmt.Printf("%-4s %s%s\n", state, s.name, extras)
		}
		return
	}

	final, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tui error:", err)
		os.Exit(1)
	}
	fm := final.(model)
	switch {
	case fm.err != nil:
		fmt.Fprintln(os.Stderr, "apply error:", fm.err)
		os.Exit(1)
	case fm.saved:
		fmt.Println("Skills synced.")
	case fm.quitMsg != "":
		fmt.Println(fm.quitMsg)
	}
}
