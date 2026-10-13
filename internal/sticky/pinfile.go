package sticky

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// pinFileVersion identifies the on-disk format. A file with another version is ignored.
const pinFileVersion = 1

// PinFile saves pins as JSON so a host restart does not send every route back to priority
// order. It holds auth IDs (credential file names) only, never credential data.
type PinFile struct {
	path string
}

// NewPinFile returns a PinFile at path, which must be absolute.
func NewPinFile(path string) PinFile {
	return PinFile{path: path}
}

type pinFileBody struct {
	Version int            `json:"version"`
	Pins    []pinFileEntry `json:"pins"`
}

type pinFileEntry struct {
	Provider string    `json:"provider"`
	Model    string    `json:"model"`
	AuthID   string    `json:"auth_id"`
	PinnedAt time.Time `json:"pinned_at"`
}

// Load reads the saved pins. A missing file is not an error and yields no pins; entries
// without a provider, model or auth ID are dropped.
func (f PinFile) Load() ([]SavedPin, error) {
	raw, errRead := os.ReadFile(f.path)
	if errors.Is(errRead, fs.ErrNotExist) {
		return nil, nil
	}
	if errRead != nil {
		return nil, fmt.Errorf("read pins file: %w", errRead)
	}
	var body pinFileBody
	if errUnmarshal := json.Unmarshal(raw, &body); errUnmarshal != nil {
		return nil, fmt.Errorf("decode pins file: %w", errUnmarshal)
	}
	if body.Version != pinFileVersion {
		return nil, fmt.Errorf("pins file version %d, want %d", body.Version, pinFileVersion)
	}
	pins := make([]SavedPin, 0, len(body.Pins))
	for _, entry := range body.Pins {
		provider, model := normalizeProvider(entry.Provider), baseModel(entry.Model)
		if provider == "" || model == "" || entry.AuthID == "" {
			continue
		}
		pins = append(pins, SavedPin{Provider: provider, Model: model, AuthID: entry.AuthID, PinnedAt: entry.PinnedAt})
	}
	return pins, nil
}

// Save replaces the file atomically (write to a temporary file, then rename), with mode 0600.
func (f PinFile) Save(pins []SavedPin) error {
	body := pinFileBody{Version: pinFileVersion, Pins: make([]pinFileEntry, 0, len(pins))}
	for _, p := range pins {
		body.Pins = append(body.Pins, pinFileEntry{Provider: p.Provider, Model: p.Model, AuthID: p.AuthID, PinnedAt: p.PinnedAt.UTC()})
	}
	raw, errMarshal := json.MarshalIndent(body, "", "  ")
	if errMarshal != nil {
		return fmt.Errorf("encode pins file: %w", errMarshal)
	}

	tmp, errCreate := os.CreateTemp(filepath.Dir(f.path), filepath.Base(f.path)+".*.tmp")
	if errCreate != nil {
		return fmt.Errorf("create pins file: %w", errCreate)
	}
	tmpPath := tmp.Name()
	_, errWrite := tmp.Write(append(raw, '\n'))
	if errWrite == nil {
		errWrite = tmp.Sync()
	}
	if errClose := tmp.Close(); errWrite == nil {
		errWrite = errClose
	}
	if errWrite == nil {
		errWrite = os.Rename(tmpPath, f.path)
	}
	if errWrite != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write pins file: %w", errWrite)
	}
	return nil
}
