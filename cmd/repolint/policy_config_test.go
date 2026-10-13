package main

import "testing"

func TestArchitecturePolicyValidation(t *testing.T) {
	for _, config := range []string{
		"[rules.native-call-lock]\nguard='mu+'",
		"[rules.native-call-lock]\ncalls=[]",
		"[rules.native-allocation]\nallocators={bad='free'}",
		"[rules.native-length]\nsources=['[']",
		"[rules.blocking-under-lock]\nlocks=[]",
		"[rules.subprocess-context]\ncontext_arg=-1",
		"[rules.suppression-policy]\nfix='safe'",
		"[rules.schema-drift]\nenabled=true",
		"[rules.native-call-lock]\nexclude_functions=['[']",
	} {
		t.Run(config, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, ".repolint.toml", "schema=1\n"+config)
			if _, _, configErr := loadConfig(root, ""); configErr == nil {
				t.Fatal("invalid policy accepted")
			}
		})
	}
}
