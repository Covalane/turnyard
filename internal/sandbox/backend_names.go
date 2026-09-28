package sandbox

const (
	BackendAppleContainer = "apple-container"
	BackendDocker         = "docker"
	BackendPodman         = "podman"
)

// BuiltinBackends returns the built-in OCI backends in display order.
func BuiltinBackends() []string {
	return []string{BackendAppleContainer, BackendDocker, BackendPodman}
}
