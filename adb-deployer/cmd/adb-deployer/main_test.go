package main

import "testing"

func TestModeFlagOverridesEnvironmentAndDefaultsToHost(t *testing.T) {
	for _, test := range []struct {
		name string
		env  string
		args []string
		want string
	}{
		{name: "default", want: "host"},
		{name: "environment", env: "device", want: "device"},
		{name: "flag", env: "device", args: []string{"--mode=host"}, want: "host"},
		{name: "device flag", args: []string{"--mode=device"}, want: "device"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := parseConfig(test.args, func(key string) string {
				if key == "SCRCPYCAT_ADB_MODE" {
					return test.env
				}
				return ""
			})
			if err != nil || config.Mode != test.want {
				t.Fatalf("mode=%q error=%v", config.Mode, err)
			}
		})
	}
}
