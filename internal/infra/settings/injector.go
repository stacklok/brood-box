// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package settings implements host-to-guest settings injection by copying,
// filtering, and merging configuration files into the guest rootfs.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/stacklok/brood-box/pkg/domain/settings"
)

const (
	// sandboxHome is the home directory of the sandbox user inside the guest rootfs.
	sandboxHome = "home/sandbox"

	// sandboxUID is the UID of the sandbox user inside the guest.
	sandboxUID = 1000
	// sandboxGID is the GID of the sandbox user inside the guest.
	sandboxGID = 1000

	dirPerm  = 0700
	filePerm = 0600
)

// FSInjector copies filtered settings from the host into the guest rootfs.
type FSInjector struct {
	logger *slog.Logger
}

// NewFSInjector creates a new filesystem-backed settings injector.
func NewFSInjector(logger *slog.Logger) *FSInjector {
	return &FSInjector{logger: logger}
}

// counters tracks aggregate limits across all manifest entries.
type counters struct {
	fileCount int
	totalSize int64
}

// Inject processes each entry in the manifest, copying files from hostHomeDir
// into rootfsPath according to each entry's Kind.
func (f *FSInjector) Inject(rootfsPath, hostHomeDir string, manifest settings.Manifest) (settings.InjectionResult, error) {
	c := &counters{}

	for _, entry := range manifest.Entries {
		if err := f.processEntry(rootfsPath, hostHomeDir, entry, c); err != nil {
			return settings.InjectionResult{}, fmt.Errorf("injecting %q: %w", entry.HostPath, err)
		}
	}

	result := settings.InjectionResult{
		FileCount:  c.fileCount,
		TotalBytes: c.totalSize,
	}

	f.logger.Info("settings injection complete",
		"files", result.FileCount, "total_bytes", result.TotalBytes)
	return result, nil
}

// Extract copies settings files from the guest rootfs back to the host
// home directory. For KindFile and KindDirectory entries the guest file is
// copied directly. For KindMergeFile entries the guest file is only written
// when the host file does not already exist — this prevents overwriting
// filtered fields that were stripped during injection.
func (f *FSInjector) Extract(rootfsPath, hostHomeDir string, manifest settings.Manifest) (settings.InjectionResult, error) {
	c := &counters{}

	for _, entry := range manifest.Entries {
		if err := f.extractEntry(rootfsPath, hostHomeDir, entry, c); err != nil {
			return settings.InjectionResult{}, fmt.Errorf("extracting %q: %w", entry.GuestPath, err)
		}
	}

	result := settings.InjectionResult{
		FileCount:  c.fileCount,
		TotalBytes: c.totalSize,
	}

	f.logger.Info("settings extraction complete",
		"files", result.FileCount, "total_bytes", result.TotalBytes)
	return result, nil
}

func (f *FSInjector) extractEntry(
	rootfsPath, hostHomeDir string,
	entry settings.Entry,
	c *counters,
) error {
	guestPath := filepath.Join(rootfsPath, sandboxHome, entry.GuestPath)

	info, err := os.Lstat(guestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			f.logger.Debug("guest file not found, skipping extraction", "path", entry.GuestPath)
			return nil
		}
		return fmt.Errorf("stat guest file: %w", err)
	}

	// Skip symlinks.
	if info.Mode()&os.ModeSymlink != 0 {
		f.logger.Warn("skipping symlink during extraction", "path", entry.GuestPath)
		return nil
	}

	// For merge files, only extract if the host file does NOT exist.
	// This prevents overwriting fields that were filtered during injection.
	if entry.Kind == settings.KindMergeFile {
		hostPath := filepath.Join(hostHomeDir, entry.HostPath)
		if _, statErr := os.Lstat(hostPath); statErr == nil {
			f.logger.Debug("host merge file exists, skipping extraction to avoid overwriting filtered fields",
				"path", entry.HostPath)
			return nil
		}
	}

	if info.IsDir() {
		return f.extractDirectory(rootfsPath, hostHomeDir, entry, c, 0)
	}
	return f.extractFile(rootfsPath, hostHomeDir, entry, c)
}

func (f *FSInjector) extractFile(
	rootfsPath, hostHomeDir string,
	entry settings.Entry,
	c *counters,
) error {
	guestPath := filepath.Join(rootfsPath, sandboxHome, entry.GuestPath)

	if err := validateContainment(filepath.Join(rootfsPath, sandboxHome), guestPath); err != nil {
		return fmt.Errorf("guest path containment: %w", err)
	}

	data, err := readFileNoFollow(guestPath)
	if err != nil {
		return fmt.Errorf("reading guest file: %w", err)
	}

	dataSize := int64(len(data))
	if dataSize > settings.MaxFileSize {
		return fmt.Errorf("file %q exceeds max size (%d > %d)", entry.GuestPath, dataSize, settings.MaxFileSize)
	}
	if c.fileCount >= settings.MaxFileCount {
		return fmt.Errorf("file count would exceed limit (%d)", settings.MaxFileCount)
	}
	if c.totalSize+dataSize > settings.MaxTotalSize {
		return fmt.Errorf("aggregate size would exceed limit (%d)", settings.MaxTotalSize)
	}

	dstPath := filepath.Join(hostHomeDir, entry.HostPath)

	if err := validateContainment(hostHomeDir, dstPath); err != nil {
		return fmt.Errorf("host path containment: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), dirPerm); err != nil {
		return fmt.Errorf("creating parent dirs: %w", err)
	}

	if err := os.WriteFile(dstPath, data, filePerm); err != nil {
		return fmt.Errorf("writing host file: %w", err)
	}

	c.fileCount++
	c.totalSize += dataSize

	f.logger.Debug("extracted file", "src", entry.GuestPath, "dst", entry.HostPath)
	return nil
}

func (f *FSInjector) extractDirectory(
	rootfsPath, hostHomeDir string,
	entry settings.Entry,
	c *counters,
	depth int,
) error {
	if depth >= settings.MaxDepth {
		f.logger.Warn("skipping directory exceeding max depth during extraction", "path", entry.GuestPath, "depth", depth)
		return nil
	}

	guestPath := filepath.Join(rootfsPath, sandboxHome, entry.GuestPath)

	if err := validateContainment(filepath.Join(rootfsPath, sandboxHome), guestPath); err != nil {
		return fmt.Errorf("guest path containment: %w", err)
	}

	entries, err := os.ReadDir(guestPath)
	if err != nil {
		return fmt.Errorf("reading guest directory: %w", err)
	}

	dstPath := filepath.Join(hostHomeDir, entry.HostPath)

	if err := validateContainment(hostHomeDir, dstPath); err != nil {
		return fmt.Errorf("host path containment: %w", err)
	}

	if err := os.MkdirAll(dstPath, dirPerm); err != nil {
		return fmt.Errorf("creating host directory: %w", err)
	}

	for _, de := range entries {
		childGuestPath := filepath.Join(entry.GuestPath, de.Name())
		childHostPath := filepath.Join(entry.HostPath, de.Name())

		childInfo, err := os.Lstat(filepath.Join(rootfsPath, sandboxHome, childGuestPath))
		if err != nil {
			return fmt.Errorf("lstat %q: %w", childGuestPath, err)
		}

		if childInfo.Mode()&os.ModeSymlink != 0 {
			f.logger.Warn("skipping symlink during extraction", "path", childGuestPath)
			continue
		}

		childEntry := settings.Entry{
			HostPath:  childHostPath,
			GuestPath: childGuestPath,
		}

		if childInfo.IsDir() {
			childEntry.Kind = settings.KindDirectory
			if err := f.extractDirectory(rootfsPath, hostHomeDir, childEntry, c, depth+1); err != nil {
				return err
			}
		} else {
			childEntry.Kind = settings.KindFile
			if err := f.extractFile(rootfsPath, hostHomeDir, childEntry, c); err != nil {
				return err
			}
		}
	}

	return nil
}

func (f *FSInjector) processEntry(
	rootfsPath, hostHomeDir string,
	entry settings.Entry,
	c *counters,
) error {
	switch entry.Kind {
	case settings.KindFile:
		return f.injectFile(rootfsPath, hostHomeDir, entry, c)
	case settings.KindDirectory:
		return f.injectDirectory(rootfsPath, hostHomeDir, entry, c, 0)
	case settings.KindMergeFile:
		return f.injectMergeFile(rootfsPath, hostHomeDir, entry, c)
	default:
		return fmt.Errorf("unknown entry kind: %d", entry.Kind)
	}
}

// injectFile copies a single file from host to guest.
func (f *FSInjector) injectFile(
	rootfsPath, hostHomeDir string,
	entry settings.Entry,
	c *counters,
) error {
	srcPath := filepath.Join(hostHomeDir, entry.HostPath)

	// Validate source stays within host home (defense-in-depth).
	if err := validateContainment(hostHomeDir, srcPath); err != nil {
		return fmt.Errorf("source path containment: %w", err)
	}

	info, err := os.Lstat(srcPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && entry.Optional {
			f.logger.Debug("optional file not found, skipping", "path", entry.HostPath)
			return nil
		}
		return fmt.Errorf("stat source: %w", err)
	}

	// Skip symlinks.
	if info.Mode()&os.ModeSymlink != 0 {
		f.logger.Warn("skipping symlink", "path", srcPath)
		return nil
	}

	if info.Size() > settings.MaxFileSize {
		return fmt.Errorf("file %q exceeds max size (%d > %d)", entry.HostPath, info.Size(), settings.MaxFileSize)
	}

	if c.fileCount >= settings.MaxFileCount {
		return fmt.Errorf("file count would exceed limit (%d)", settings.MaxFileCount)
	}

	dstPath := filepath.Join(rootfsPath, sandboxHome, entry.GuestPath)

	if err := validateContainment(filepath.Join(rootfsPath, sandboxHome), dstPath); err != nil {
		return fmt.Errorf("path containment: %w", err)
	}

	// Open with O_NOFOLLOW to prevent TOCTOU symlink attacks between
	// the Lstat check and the actual file read.
	data, err := readFileNoFollow(srcPath)
	if err != nil {
		return fmt.Errorf("reading source: %w", err)
	}

	// Use actual read size for aggregate tracking (not stat size) to
	// prevent TOCTOU between stat and read.
	dataSize := int64(len(data))
	if dataSize > settings.MaxFileSize {
		return fmt.Errorf("file %q read size exceeds max (%d > %d)", entry.HostPath, dataSize, settings.MaxFileSize)
	}
	if c.totalSize+dataSize > settings.MaxTotalSize {
		return fmt.Errorf("aggregate size would exceed limit (%d)", settings.MaxTotalSize)
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), dirPerm); err != nil {
		return fmt.Errorf("creating parent dirs: %w", err)
	}
	bestEffortChown(f.logger, filepath.Dir(dstPath))

	if err := os.WriteFile(dstPath, data, filePerm); err != nil {
		return fmt.Errorf("writing destination: %w", err)
	}
	bestEffortChown(f.logger, dstPath)

	c.fileCount++
	c.totalSize += dataSize

	f.logger.Debug("injected file", "src", entry.HostPath, "dst", entry.GuestPath)
	return nil
}

// injectDirectory recursively copies a directory from host to guest.
func (f *FSInjector) injectDirectory(
	rootfsPath, hostHomeDir string,
	entry settings.Entry,
	c *counters,
	depth int,
) error {
	if depth >= settings.MaxDepth {
		f.logger.Warn("skipping directory exceeding max depth", "path", entry.HostPath, "depth", depth)
		return nil
	}

	srcPath := filepath.Join(hostHomeDir, entry.HostPath)

	// Validate source stays within host home (defense-in-depth).
	if err := validateContainment(hostHomeDir, srcPath); err != nil {
		return fmt.Errorf("source path containment: %w", err)
	}

	info, err := os.Lstat(srcPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && entry.Optional {
			f.logger.Debug("optional directory not found, skipping", "path", entry.HostPath)
			return nil
		}
		return fmt.Errorf("stat source dir: %w", err)
	}

	// Skip symlinks.
	if info.Mode()&os.ModeSymlink != 0 {
		f.logger.Warn("skipping symlink directory", "path", srcPath)
		return nil
	}

	dstPath := filepath.Join(rootfsPath, sandboxHome, entry.GuestPath)

	if err := validateContainment(filepath.Join(rootfsPath, sandboxHome), dstPath); err != nil {
		return fmt.Errorf("path containment: %w", err)
	}

	if err := os.MkdirAll(dstPath, dirPerm); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	bestEffortChown(f.logger, dstPath)

	entries, err := os.ReadDir(srcPath)
	if err != nil {
		return fmt.Errorf("reading directory: %w", err)
	}

	for _, de := range entries {
		childHostPath := filepath.Join(entry.HostPath, de.Name())
		childGuestPath := filepath.Join(entry.GuestPath, de.Name())

		childEntry := settings.Entry{
			HostPath:  childHostPath,
			GuestPath: childGuestPath,
			Optional:  entry.Optional,
		}

		childInfo, err := os.Lstat(filepath.Join(hostHomeDir, childHostPath))
		if err != nil {
			return fmt.Errorf("lstat %q: %w", childHostPath, err)
		}

		// Skip symlinks.
		if childInfo.Mode()&os.ModeSymlink != 0 {
			f.logger.Warn("skipping symlink", "path", childHostPath)
			continue
		}

		if childInfo.IsDir() {
			childEntry.Kind = settings.KindDirectory
			if err := f.injectDirectory(rootfsPath, hostHomeDir, childEntry, c, depth+1); err != nil {
				return err
			}
		} else {
			childEntry.Kind = settings.KindFile
			if err := f.injectFile(rootfsPath, hostHomeDir, childEntry, c); err != nil {
				return err
			}
		}
	}

	return nil
}

// injectMergeFile reads a host config file, filters it, and deep-merges
// the result into an existing guest file.
func (f *FSInjector) injectMergeFile(
	rootfsPath, hostHomeDir string,
	entry settings.Entry,
	c *counters,
) error {
	srcPath := filepath.Join(hostHomeDir, entry.HostPath)

	// Validate source stays within host home (defense-in-depth).
	if err := validateContainment(hostHomeDir, srcPath); err != nil {
		return fmt.Errorf("source path containment: %w", err)
	}

	info, err := os.Lstat(srcPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && entry.Optional {
			f.logger.Debug("optional merge file not found, skipping", "path", entry.HostPath)
			return nil
		}
		return fmt.Errorf("stat source: %w", err)
	}

	// Skip symlinks.
	if info.Mode()&os.ModeSymlink != 0 {
		f.logger.Warn("skipping symlink", "path", srcPath)
		return nil
	}

	if info.Size() > settings.MaxFileSize {
		return fmt.Errorf("file %q exceeds max size (%d > %d)", entry.HostPath, info.Size(), settings.MaxFileSize)
	}

	if c.fileCount >= settings.MaxFileCount {
		return fmt.Errorf("file count would exceed limit (%d)", settings.MaxFileCount)
	}

	dstPath := filepath.Join(rootfsPath, sandboxHome, entry.GuestPath)

	if err := validateContainment(filepath.Join(rootfsPath, sandboxHome), dstPath); err != nil {
		return fmt.Errorf("path containment: %w", err)
	}

	// Open with O_NOFOLLOW to prevent TOCTOU symlink attacks.
	rawHost, err := readFileNoFollow(srcPath)
	if err != nil {
		return fmt.Errorf("reading host file: %w", err)
	}

	hostData, err := parseConfig(rawHost, entry.Format)
	if err != nil {
		return fmt.Errorf("parsing host file %q: %w", entry.HostPath, err)
	}

	// Apply filters.
	if entry.Filter != nil {
		hostData = applyFilter(hostData, entry.Filter)
	}

	// Read existing guest file (may have been placed by credential injection).
	var guestData map[string]any
	rawGuest, err := os.ReadFile(dstPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("reading existing guest file: %w", err)
		}
		guestData = make(map[string]any)
	} else {
		guestData, err = parseConfig(rawGuest, entry.Format)
		if err != nil {
			return fmt.Errorf("parsing existing guest file: %w", err)
		}
	}

	// Deep-merge: host data overrides guest data.
	merged := deepMerge(guestData, hostData)

	// Serialize back.
	output, err := serializeConfig(merged, entry.Format)
	if err != nil {
		return fmt.Errorf("serializing merged config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), dirPerm); err != nil {
		return fmt.Errorf("creating parent dirs: %w", err)
	}
	bestEffortChown(f.logger, filepath.Dir(dstPath))

	if err := os.WriteFile(dstPath, output, filePerm); err != nil {
		return fmt.Errorf("writing merged file: %w", err)
	}
	bestEffortChown(f.logger, dstPath)

	c.fileCount++
	c.totalSize += int64(len(output))

	f.logger.Debug("injected merge file", "src", entry.HostPath, "dst", entry.GuestPath)
	return nil
}

// parseConfig parses data according to format into a generic map.
func parseConfig(data []byte, format string) (map[string]any, error) {
	switch format {
	case "json":
		return parseJSON(data)
	case "jsonc":
		stripped := stripJSONC(data)
		return parseJSON(stripped)
	case "toml":
		return parseTOML(data)
	default:
		return nil, fmt.Errorf("unsupported format: %q", format)
	}
}

func parseJSON(data []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}
	return m, nil
}

func parseTOML(data []byte) (map[string]any, error) {
	var m map[string]any
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing TOML: %w", err)
	}
	return m, nil
}

// serializeConfig writes data back in the given format.
func serializeConfig(data map[string]any, format string) ([]byte, error) {
	switch format {
	case "json", "jsonc":
		out, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshaling JSON: %w", err)
		}
		// Append trailing newline for consistency.
		return append(out, '\n'), nil
	case "toml":
		out, err := toml.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("marshaling TOML: %w", err)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported format: %q", format)
	}
}

// applyFilter keeps only allowed top-level keys and strips denied sub-keys.
func applyFilter(data map[string]any, filter *settings.FieldFilter) map[string]any {
	if len(filter.AllowKeys) == 0 && len(filter.DenySubKeys) == 0 {
		return data
	}

	result := make(map[string]any)

	// If AllowKeys is set, keep only those top-level keys.
	if len(filter.AllowKeys) > 0 {
		allowed := make(map[string]bool, len(filter.AllowKeys))
		for _, k := range filter.AllowKeys {
			allowed[k] = true
		}
		for k, v := range data {
			if allowed[k] {
				result[k] = v
			}
		}
	} else {
		// Copy all keys.
		for k, v := range data {
			result[k] = v
		}
	}

	// Apply deny sub-key patterns.
	for topKey, patterns := range filter.DenySubKeys {
		val, ok := result[topKey]
		if !ok {
			continue
		}
		result[topKey] = applyDenySubKeys(val, patterns)
	}

	return result
}

// applyDenySubKeys removes matching sub-keys from the value, recursing into
// nested maps so that patterns like "*.apiKey" strip the key at all depths.
func applyDenySubKeys(val any, patterns []string) any {
	topMap, ok := val.(map[string]any)
	if !ok {
		return val
	}

	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "*.") {
			subKey := strings.TrimPrefix(pattern, "*.")
			stripKeyRecursive(topMap, subKey)
		} else {
			delete(topMap, pattern)
		}
	}

	return topMap
}

// stripKeyRecursive removes subKey from m and from every nested map within m.
func stripKeyRecursive(m map[string]any, subKey string) {
	delete(m, subKey)
	for _, v := range m {
		if nested, ok := v.(map[string]any); ok {
			stripKeyRecursive(nested, subKey)
		}
	}
}

// deepMerge recursively merges src into dst. Values from src override dst.
// Both maps are treated as nested map[string]any structures.
// Recursion is bounded by settings.MaxDepth to prevent stack overflow.
func deepMerge(dst, src map[string]any) map[string]any {
	return deepMergeN(dst, src, 0)
}

func deepMergeN(dst, src map[string]any, depth int) map[string]any {
	result := make(map[string]any, len(dst))
	for k, v := range dst {
		result[k] = v
	}

	for k, srcVal := range src {
		dstVal, exists := result[k]
		if !exists {
			result[k] = srcVal
			continue
		}

		srcMap, srcIsMap := srcVal.(map[string]any)
		dstMap, dstIsMap := dstVal.(map[string]any)

		if srcIsMap && dstIsMap && depth < settings.MaxDepth {
			result[k] = deepMergeN(dstMap, srcMap, depth+1)
		} else {
			result[k] = srcVal
		}
	}

	return result
}

// validateContainment ensures target stays under base directory.
// Used for both source (host home) and destination (rootfs) paths.
func validateContainment(base, target string) error {
	cleaned := filepath.Clean(target)

	rel, err := filepath.Rel(base, cleaned)
	if err != nil {
		return fmt.Errorf("computing relative path: %w", err)
	}

	if strings.HasPrefix(rel, "..") {
		return fmt.Errorf("path %q escapes base %q", target, base)
	}

	// Resolve symlinks where possible to prevent symlinked intermediate
	// directories from escaping containment.
	resolvedTarget, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Target doesn't exist yet — resolve parent instead.
			resolvedDir, dirErr := filepath.EvalSymlinks(filepath.Dir(cleaned))
			if dirErr != nil {
				if errors.Is(dirErr, os.ErrNotExist) {
					return nil // parent doesn't exist either; safe
				}
				return fmt.Errorf("resolving parent symlinks: %w", dirErr)
			}
			resolvedTarget = filepath.Join(resolvedDir, filepath.Base(cleaned))
		} else {
			return fmt.Errorf("resolving target symlinks: %w", err)
		}
	}

	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("resolving base symlinks: %w", err)
	}

	if !strings.HasPrefix(resolvedTarget+"/", resolvedBase+"/") {
		return fmt.Errorf("resolved path %q escapes base %q", resolvedTarget, resolvedBase)
	}

	return nil
}

// readFileNoFollow opens a file with O_NOFOLLOW to prevent TOCTOU symlink
// attacks between the Lstat check and the actual read.
func readFileNoFollow(path string) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open (O_NOFOLLOW): %w", err)
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("reading file: %w", err)
	}
	return data, nil
}

// bestEffortChown attempts to chown a path to the sandbox user, ignoring EPERM.
func bestEffortChown(logger *slog.Logger, path string) {
	err := os.Lchown(path, sandboxUID, sandboxGID)
	if err != nil && !errors.Is(err, syscall.EPERM) && !errors.Is(err, fs.ErrPermission) {
		logger.Debug("unexpected chown error", "path", path, "error", err)
	}
}
