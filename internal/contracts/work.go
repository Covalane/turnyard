package contracts

import (
	"encoding/json"
	"mime"
	"net/url"
	posixpath "path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Covalane/turnyard/internal/fault"
)

var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ValidateWork(path string, session SessionSpec, env EnvironmentSpec) (WorkSpec, error) {
	var work WorkSpec
	if err := ReadJSON(path, "work", &work); err != nil {
		return work, err
	}
	return ValidateWorkSpec(work, session, env)
}

// ValidateWorkSpec validates a work item built by a trusted internal caller.
// It applies the same JSON schema and semantic checks as the file input path.
func ValidateWorkSpec(work WorkSpec, session SessionSpec, env EnvironmentSpec) (WorkSpec, error) {
	var value any
	if err := json.Unmarshal([]byte(JSONText(work)), &value); err != nil {
		return work, err
	}
	schema, err := schemaFor("work")
	if err != nil {
		return work, err
	}
	if err := schema.Validate(value); err != nil {
		return work, fault.Wrap(fault.CodeInvalidSpec, "", err, "work")
	}
	if err := ValidateCommitMessage(work.CommitMessage); err != nil {
		return work, err
	}
	if len(work.Checks) == 0 && len(work.Deliverables) == 0 {
		return work, fault.New(fault.CodeInvalidSpec, "work requires at least one trusted check or deliverable")
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, r := range work.Scope.Repositories {
		ids = append(ids, r.ID)
		seen[r.ID] = true
	}
	if err := uniqueIDs(ids, "scope repository"); err != nil {
		return work, err
	}
	if len(seen) != len(session.Repositories) {
		return work, fault.New(fault.CodeInvalidSpec, "scope must name every repository")
	}
	for _, r := range session.Repositories {
		if !seen[r.ID] {
			return work, fault.New(fault.CodeInvalidSpec, "scope missing repository %s", r.ID)
		}
	}
	checks := map[string]bool{}
	for _, c := range env.Checks {
		checks[c.ID] = true
	}
	for _, id := range work.Checks {
		if !checks[id] {
			return work, fault.New(fault.CodeInvalidSpec, "unknown trusted check: %s", id)
		}
	}
	writable := map[string]bool{}
	for _, r := range work.Scope.Repositories {
		writable[r.ID] = r.Mode == ScopeWrite
	}
	connectors := map[string]ArtifactConnectorSpec{}
	for _, connector := range env.ArtifactConnectors {
		connectors[connector.ID] = connector
	}
	inputIDs := map[string]bool{}
	for _, input := range work.Inputs {
		if inputIDs[input.ID] {
			return work, fault.New(fault.CodeInvalidSpec, "duplicate input ID: %s", input.ID)
		}
		inputIDs[input.ID] = true
		if input.ExpectedSHA256 != "" && !sha256Pattern.MatchString(input.ExpectedSHA256) {
			return work, fault.New(fault.CodeInvalidSpec, "input %s has an invalid SHA-256", input.ID)
		}
		if !validMediaType(input.MediaType) {
			return work, fault.New(fault.CodeInvalidSpec, "input %s has an invalid media type", input.ID)
		}
		source := input.Source
		switch source.Kind {
		case InputFile:
			if source.Path == "" || source.URL != "" || source.Connector != "" || source.URI != "" {
				return work, fault.New(fault.CodeInvalidSpec, "input %s has invalid file source", input.ID)
			}
		case InputHTTPS:
			u, err := url.Parse(source.URL)
			if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || source.Path != "" || source.Connector != "" || source.URI != "" || input.ExpectedSHA256 == "" {
				return work, fault.New(fault.CodeInvalidSpec, "input %s requires a pinned HTTPS URL", input.ID)
			}
			allowed := false
			for _, host := range env.InputHosts {
				allowed = allowed || strings.EqualFold(host, u.Hostname())
			}
			if !allowed {
				return work, fault.New(fault.CodeInvalidSpec, "input %s HTTPS host is not allowed", input.ID)
			}
		case InputConnector:
			connector, ok := connectors[source.Connector]
			if !ok || !ValidConnectorURI(connector, source.URI) || source.Path != "" || source.URL != "" || input.ExpectedSHA256 == "" {
				return work, fault.New(fault.CodeInvalidSpec, "input %s requires a pinned configured connector source", input.ID)
			}
		default:
			return work, fault.New(fault.CodeInvalidSpec, "input %s has unsupported source", input.ID)
		}
	}
	outputIDs, outputPaths := map[string]bool{}, map[string]bool{}
	for _, item := range work.Deliverables {
		if outputIDs[item.ID] {
			return work, fault.New(fault.CodeInvalidSpec, "duplicate deliverable ID: %s", item.ID)
		}
		outputIDs[item.ID] = true
		if item.ExpectedSHA256 != "" && !sha256Pattern.MatchString(item.ExpectedSHA256) {
			return work, fault.New(fault.CodeInvalidSpec, "deliverable %s has an invalid SHA-256", item.ID)
		}
		if !validMediaType(item.MediaType) {
			return work, fault.New(fault.CodeInvalidSpec, "deliverable %s has an invalid media type", item.ID)
		}
		switch item.Kind {
		case DeliverableFile, DeliverableImage:
			if item.Repository != "" && !writable[item.Repository] {
				return work, fault.New(fault.CodeInvalidSpec, "deliverable %s requires a writable repository", item.ID)
			}
			if !safeDeliverablePath(item.Path) {
				return work, fault.New(fault.CodeInvalidSpec, "deliverable %s has an unsafe path", item.ID)
			}
			key := item.Repository + ":" + item.Path
			if outputPaths[key] {
				return work, fault.New(fault.CodeInvalidSpec, "duplicate deliverable path: %s", key)
			}
			outputPaths[key] = true
			if item.Repository == "" && item.Destination == nil {
				return work, fault.New(fault.CodeInvalidSpec, "deliverable %s outside Git requires a destination", item.ID)
			}
			if err := validateDestination(item, connectors); err != nil {
				return work, err
			}
			if item.Base != "" ||
				(item.Kind == DeliverableImage && item.MediaType != "" && !supportedImageType(item.MediaType)) {
				return work, fault.New(fault.CodeInvalidSpec, "deliverable %s has incompatible fields", item.ID)
			}
		case DeliverableText:
			if item.Repository != "" || item.Path != "" || item.MediaType != "" || item.Base != "" || item.Destination != nil {
				return work, fault.New(fault.CodeInvalidSpec, "text deliverable %s has incompatible fields", item.ID)
			}
		case DeliverablePullRequest:
			if !writable[item.Repository] || item.Path != "" || item.ExpectedSHA256 != "" || item.MediaType != "" || item.Destination != nil || !safeBranchName(item.Base) {
				return work, fault.New(fault.CodeInvalidSpec, "pull request deliverable %s has invalid fields", item.ID)
			}
		default:
			return work, fault.New(fault.CodeInvalidSpec, "unsupported deliverable kind %s", item.Kind)
		}
	}
	return work, nil
}

func validMediaType(value string) bool {
	if value == "" {
		return true
	}
	parsed, _, err := mime.ParseMediaType(value)
	return err == nil && strings.Contains(parsed, "/")
}

func validateDestination(item DeliverableSpec, connectors map[string]ArtifactConnectorSpec) error {
	if item.Destination == nil {
		return nil
	}
	dest := item.Destination
	switch dest.Kind {
	case DestinationLocal:
		if item.Repository == "" && dest.Connector == "" && dest.URI == "" {
			return nil
		}
	case DestinationConnector:
		connector, ok := connectors[dest.Connector]
		if ok && len(connector.PutArgv) > 0 && strings.Count(dest.URI, "{sha256}") == 1 && ValidConnectorURI(connector, strings.Replace(dest.URI, "{sha256}", strings.Repeat("0", 64), 1)) {
			return nil
		}
	}
	return fault.New(fault.CodeInvalidSpec, "deliverable %s has invalid destination", item.ID)
}

func safeDeliverablePath(path string) bool {
	if path == "" || path == "." || posixpath.Clean(path) != path || strings.HasPrefix(path, "/") || strings.Contains(path, "\\") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == ".git" || segment == ".." {
			return false
		}
	}
	return true
}

func safeBranchName(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.Contains(name, "..") || strings.Contains(name, "//") || strings.Contains(name, "@{") || strings.HasSuffix(name, ".lock") {
		return false
	}
	for _, segment := range strings.Split(name, "/") {
		if strings.HasPrefix(segment, ".") || strings.HasSuffix(segment, ".") {
			return false
		}
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_./-", r)) {
			return false
		}
	}
	return true
}

func supportedImageType(mediaType string) bool {
	switch mediaType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}

// ValidateCommitMessage checks the optional public Git subject before it can
// be written to a repository. An empty value lets older work use the objective
// fallback; callers that set a value must use a short printable single line.
func ValidateCommitMessage(subject string) error {
	if subject == "" {
		return nil
	}
	if subject != strings.TrimSpace(subject) || utf8.RuneCountInString(subject) > 80 {
		return fault.New(fault.CodeInvalidSpec, "commitMessage must be one trimmed line of at most 80 characters")
	}
	for _, r := range subject {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return fault.New(fault.CodeInvalidSpec, "commitMessage contains a control or invisible character")
		}
	}
	return nil
}
