package config

import "strings"

// RewriteImageForProxy rewrites a container image reference to go through a crproxy-style proxy.
// If proxyHost is empty, the image is returned unchanged.
//
// Examples (proxyHost = "proxy.example.com"):
//
//	"ubuntu:latest"                             → "proxy.example.com/docker.io/library/ubuntu:latest"
//	"myuser/myimage:v1"                         → "proxy.example.com/docker.io/myuser/myimage:v1"
//	"registry.gitlab.com/org/img:tag"           → "proxy.example.com/registry.gitlab.com/org/img:tag"
//	"gcr.io/project/image:tag"                  → "proxy.example.com/gcr.io/project/image:tag"
func RewriteImageForProxy(proxyHost, image string) string {
	if proxyHost == "" || image == "" {
		return image
	}

	// Normalize: strip trailing slash from proxy host
	proxyHost = strings.TrimRight(proxyHost, "/")

	// If image already starts with the proxy host, don't double-rewrite
	if strings.HasPrefix(image, proxyHost+"/") {
		return image
	}

	// Parse image into registry + path components.
	// Docker convention: if the first path component contains a dot or colon,
	// it's treated as a registry hostname. Otherwise it's a Docker Hub image.
	registry, remainder := splitRegistryFromImage(image)

	return proxyHost + "/" + registry + "/" + remainder
}

// splitRegistryFromImage splits an image reference into (registry, remainder).
// For Docker Hub images without an explicit registry:
//   - "ubuntu:latest"       → ("docker.io", "library/ubuntu:latest")
//   - "myuser/myimage:v1"   → ("docker.io", "myuser/myimage:v1")
//
// For images with an explicit registry:
//   - "registry.gitlab.com/org/img:tag" → ("registry.gitlab.com", "org/img:tag")
//   - "gcr.io/project/image:tag"        → ("gcr.io", "project/image:tag")
func splitRegistryFromImage(image string) (registry, remainder string) {
	// Find first slash to check if it's a registry
	slashIdx := strings.IndexByte(image, '/')
	if slashIdx == -1 {
		// No slash at all: single-name image like "ubuntu:latest"
		return "docker.io", "library/" + image
	}

	firstPart := image[:slashIdx]

	// If the first component contains a dot or colon, it's a registry hostname
	if strings.ContainsAny(firstPart, ".:") {
		return firstPart, image[slashIdx+1:]
	}

	// No dot or colon in first component: Docker Hub image like "myuser/myimage:v1"
	return "docker.io", image
}
