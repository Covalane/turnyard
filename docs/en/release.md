# Release and validation

[Documentation index](../../README.md) · [中文](../zh/release.md)

`turnyard-agent:dev` is a local development image tag, not a released version. A release records the source commit, base-image digest, four agent CLI versions, Go dependencies, and resulting image digest. The Git tag determines the product version. Historical `0.3.2` image results do not identify the current build.

Run race-enabled Go tests, vet, Staticcheck, Govulncheck, and Ent regeneration checks before release. GitHub CI also builds the current image on Linux and runs real Docker probes for mounts, read-only Git metadata, timeout cleanup, model-network isolation, and tool-secret separation. CI has no cloud-model or OSS credentials, so a green run is not a real-agent acceptance result.

The release candidate matrix includes one real tool path for each of the four agents under Docker `model-only`, multi-repository continuation, managed delegation and forced-interruption recovery, and declared-output readback. Validate gVisor, Apple `container`, and Podman on each intended deployment host. Measure gVisor CPU and memory enforcement with normal cgroups; `--ignore-cgroups` cannot prove it. OSS delivery requires upload, two independent readbacks, and cleanup in a dedicated prefix. Record the commit, image digest, runtime, model, backend, outcome, and failures for each run.

Tag and publish only for deployment profiles whose matrix passed. Pinned third-party versions make a build reviewable; check upstream releases, update the pins, and rerun relevant paths before adopting newer versions.
