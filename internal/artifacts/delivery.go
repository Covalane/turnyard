package artifacts

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/Covalane/turnyard/internal/artifactio"
	"github.com/Covalane/turnyard/internal/contracts"
	"github.com/Covalane/turnyard/internal/fault"
	"github.com/Covalane/turnyard/internal/observe"
)

// DeliverConnector first reads back a content-addressed object. This makes a
// retry safe after an upload timeout: matching bytes need no second upload.
// A conflicting object is never overwritten.
func DeliverConnector(ctx context.Context, env contracts.EnvironmentSpec, spec contracts.DeliverableSpec, result Result) Result {
	if spec.Destination == nil || spec.Destination.Kind != contracts.DestinationConnector || result.Status != StatusPresent {
		return result
	}
	result.Verification = VerificationConnectorReadback
	connector, err := artifactio.Find(env, spec.Destination.Connector)
	if err != nil {
		observe.LogFailure(ctx, "deliverable connector unavailable", fault.Ensure(err, "select delivery connector"), "deliverable_id", spec.ID)
		return unverified(result)
	}
	uri := strings.Replace(spec.Destination.URI, "{sha256}", result.SHA256, 1)
	result.URI = uri
	if result.LocalPath == "" {
		observe.LogFailure(ctx, "deliverable local source unavailable", fault.New(fault.CodeDeliverableUnverified, "verified local source is missing"), "deliverable_id", spec.ID)
		return unverified(result)
	}
	localHash, err := contracts.FileDigest(result.LocalPath)
	if err != nil || localHash != result.SHA256 {
		if err != nil {
			observe.LogFailure(ctx, "deliverable local source unreadable", fault.Wrap(fault.CodeDeliverableUnverified, "digest delivery source", err, "verified local source cannot be read"), "deliverable_id", spec.ID)
		}
		return unverified(result)
	}
	readback := filepath.Join(filepath.Dir(result.LocalPath), ".readback-"+spec.ID)
	defer os.Remove(readback)
	if err := connector.Get(ctx, uri, readback, maxOutputBytes); err == nil {
		return compareReadback(ctx, result, readback)
	}
	if err := connector.Put(ctx, result.LocalPath, uri); err != nil {
		observe.LogFailure(ctx, "deliverable upload unconfirmed", fault.Ensure(err, "upload deliverable"), "deliverable_id", spec.ID, "connector", spec.Destination.Connector)
		return unverified(result)
	}
	if err := connector.Get(ctx, uri, readback, maxOutputBytes); err != nil {
		observe.LogFailure(ctx, "deliverable readback failed", fault.Ensure(err, "read back deliverable"), "deliverable_id", spec.ID, "connector", spec.Destination.Connector)
		return unverified(result)
	}
	return compareReadback(ctx, result, readback)
}

func compareReadback(ctx context.Context, result Result, path string) Result {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxOutputBytes {
		if err != nil {
			observe.LogFailure(ctx, "deliverable readback unavailable", fault.Wrap(fault.CodeDeliverableUnverified, "inspect delivery readback", err, "readback file unavailable"), "deliverable_id", result.ID)
		}
		return unverified(result)
	}
	digest, err := contracts.FileDigest(path)
	if err != nil {
		observe.LogFailure(ctx, "deliverable readback unreadable", fault.Wrap(fault.CodeDeliverableUnverified, "digest delivery readback", err, "readback file cannot be read"), "deliverable_id", result.ID)
		return unverified(result)
	}
	if digest != result.SHA256 {
		result.Status, result.ErrorCode = StatusMismatch, string(fault.CodeDeliverableMismatch)
		return result
	}
	result.Status, result.ErrorCode = StatusPresent, ""
	return result
}

func unverified(result Result) Result {
	result.Status, result.ErrorCode = StatusUnverified, string(fault.CodeDeliverableUnverified)
	return result
}
