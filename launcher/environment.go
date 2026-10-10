package main

import "strings"

func buildChildEnvironment(environment []string, key string) []string {
	out := make([]string, 0, len(environment)+5)
	for _, item := range environment {
		name := item
		if i := strings.IndexByte(item, '='); i >= 0 {
			name = item[:i]
		}
		switch strings.ToUpper(name) {
		case "NODE_ENV", "HOSTNAME", "PORT", "DEFAULT_MODEL", "DEEPSEEK_API_KEY":
			continue
		}
		out = append(out, item)
	}
	return append(out, "NODE_ENV=production", "HOSTNAME=127.0.0.1", "PORT=3000", "DEFAULT_MODEL=deepseek:deepseek-v4-flash", "DEEPSEEK_API_KEY="+key)
}

func environmentValue(env []string, name string) (string, bool) {
	for _, item := range env {
		if i := strings.IndexByte(item, '='); i >= 0 && strings.EqualFold(item[:i], name) {
			return item[i+1:], true
		}
	}
	return "", false
}

func environmentNameCount(env []string, name string) int {
	count := 0
	for _, item := range env {
		if i := strings.IndexByte(item, '='); i >= 0 && strings.EqualFold(item[:i], name) {
			count++
		}
	}
	return count
}

func environmentHasName(env []string, name string) bool {
	for _, item := range env {
		if i := strings.IndexByte(item, '='); i >= 0 && item[:i] == name {
			return true
		}
	}
	return false
}
