package humaninput

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Covalane/turnyard/internal/fault"
)

const (
	FileName         = "human-input.json"
	SchemaVersion    = "turnyard.human-input/v1"
	maxQuestionBytes = 4096
	maxRequestBytes  = 8192
)

type Request struct {
	SchemaVersion string `json:"schema_version"`
	Question      string `json:"question"`
}

func validate(question string) error {
	if strings.TrimSpace(question) != question || question == "" || len(question) > maxQuestionBytes ||
		!utf8.ValidString(question) || strings.ContainsRune(question, 0) {
		return fault.New(fault.CodeAgentOutputInvalid, "human input question must be nonempty UTF-8 text of at most %d bytes", maxQuestionBytes)
	}
	return nil
}

// Write records at most one question in the invocation's output mount. A
// repeated identical call is safe after an uncertain tool response.
func Write(outputDir, question string) error {
	if err := validate(question); err != nil {
		return err
	}
	root, err := os.OpenRoot(outputDir)
	if err != nil {
		return fault.Wrap(fault.CodeAgentOutputInvalid, "open human input output", err, "human input output is unavailable")
	}
	defer root.Close()
	file, err := root.OpenFile(FileName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		previous, readErr := Read(outputDir)
		if readErr == nil && previous == question {
			return nil
		}
		return fault.New(fault.CodeAgentOutputInvalid, "a different human input question is already recorded")
	}
	if err != nil {
		return fault.Wrap(fault.CodeAgentOutputInvalid, "create human input request", err, "human input request could not be created")
	}
	body, err := json.Marshal(Request{SchemaVersion: SchemaVersion, Question: question})
	if err != nil {
		_ = file.Close()
		return err
	}
	_, writeErr := file.Write(body)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		_ = root.Remove(FileName)
		return fault.Wrap(fault.CodeAgentOutputInvalid, "write human input request", err, "human input request could not be saved")
	}
	return nil
}

// Read returns an empty question when the agent did not request input.
func Read(outputDir string) (string, error) {
	root, err := os.OpenRoot(outputDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fault.Wrap(fault.CodeAgentOutputInvalid, "open human input output", err, "human input output is unavailable")
	}
	defer root.Close()
	info, err := root.Lstat(FileName)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxRequestBytes {
		return "", fault.New(fault.CodeAgentOutputInvalid, "human input request must be a regular file of at most %d bytes", maxRequestBytes)
	}
	file, err := root.Open(FileName)
	if err != nil {
		return "", fault.Wrap(fault.CodeAgentOutputInvalid, "read human input request", err, "human input request is unavailable")
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxRequestBytes {
		return "", fault.New(fault.CodeAgentOutputInvalid, "human input request must be a regular file of at most %d bytes", maxRequestBytes)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxRequestBytes+1))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil || request.SchemaVersion != SchemaVersion {
		return "", fault.New(fault.CodeAgentOutputInvalid, "invalid human input request")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", fault.New(fault.CodeAgentOutputInvalid, "human input request has trailing content")
	}
	if err := validate(request.Question); err != nil {
		return "", err
	}
	return request.Question, nil
}
