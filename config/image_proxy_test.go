package config

import "testing"

func TestRewriteImageForProxy(t *testing.T) {
	proxy := "proxy.example.com"

	tests := []struct {
		name  string
		proxy string
		image string
		want  string
	}{
		{
			name:  "empty proxy returns image unchanged",
			proxy: "",
			image: "ubuntu:latest",
			want:  "ubuntu:latest",
		},
		{
			name:  "empty image returns empty",
			proxy: proxy,
			image: "",
			want:  "",
		},
		{
			name:  "single-name Docker Hub image",
			proxy: proxy,
			image: "ubuntu:latest",
			want:  "proxy.example.com/docker.io/library/ubuntu:latest",
		},
		{
			name:  "single-name image without tag",
			proxy: proxy,
			image: "alpine",
			want:  "proxy.example.com/docker.io/library/alpine",
		},
		{
			name:  "Docker Hub user/image",
			proxy: proxy,
			image: "myuser/myimage:v1",
			want:  "proxy.example.com/docker.io/myuser/myimage:v1",
		},
		{
			name:  "Docker Hub user/image without tag",
			proxy: proxy,
			image: "library/ubuntu",
			want:  "proxy.example.com/docker.io/library/ubuntu",
		},
		{
			name:  "explicit docker.io registry",
			proxy: proxy,
			image: "docker.io/library/ubuntu:latest",
			want:  "proxy.example.com/docker.io/library/ubuntu:latest",
		},
		{
			name:  "GitLab registry",
			proxy: proxy,
			image: "registry.gitlab.com/gitlab-org/gitlab-runner/gitlab-runner-helper:x86_64-v18.9.0",
			want:  "proxy.example.com/registry.gitlab.com/gitlab-org/gitlab-runner/gitlab-runner-helper:x86_64-v18.9.0",
		},
		{
			name:  "GCR registry",
			proxy: proxy,
			image: "gcr.io/project/image:tag",
			want:  "proxy.example.com/gcr.io/project/image:tag",
		},
		{
			name:  "quay.io registry",
			proxy: proxy,
			image: "quay.io/org/image:latest",
			want:  "proxy.example.com/quay.io/org/image:latest",
		},
		{
			name:  "registry with port",
			proxy: proxy,
			image: "myregistry.com:5000/myimage:latest",
			want:  "proxy.example.com/myregistry.com:5000/myimage:latest",
		},
		{
			name:  "proxy with trailing slash",
			proxy: "proxy.example.com/",
			image: "ubuntu:latest",
			want:  "proxy.example.com/docker.io/library/ubuntu:latest",
		},
		{
			name:  "already proxied image is not double-rewritten",
			proxy: proxy,
			image: "proxy.example.com/docker.io/library/ubuntu:latest",
			want:  "proxy.example.com/docker.io/library/ubuntu:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RewriteImageForProxy(tt.proxy, tt.image)
			if got != tt.want {
				t.Errorf("RewriteImageForProxy(%q, %q) = %q, want %q", tt.proxy, tt.image, got, tt.want)
			}
		})
	}
}
