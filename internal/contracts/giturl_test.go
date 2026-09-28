package contracts

import "testing"

func TestValidateGitURLRejectsCredentialsAndAmbiguousTargets(t *testing.T) {
	for _, tc := range []struct {
		url   string
		valid bool
	}{
		{"https://github.com/example/app.git", true},
		{"ssh://git@github.com/example/app.git", true},
		{"file:///tmp/app.git", true},
		{"https://user:token@github.com/example/app.git", false},
		{"https://user@github.com/example/app.git", false},
		{"https://github.com/example/app.git?token=secret", false},
		{"https://github.com/example/app.git#branch", false},
		{"file://other-host/tmp/app.git", false},
		{"/tmp/app.git", false},
	} {
		if got := ValidateGitURL(tc.url) == nil; got != tc.valid {
			t.Errorf("URL %q valid=%v, want %v", tc.url, got, tc.valid)
		}
	}
}
