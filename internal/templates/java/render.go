// Package java renders Docker artifacts for Maven and Gradle projects.
package java

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"github.com/carl/dockerizethis/internal/emit"
	"github.com/carl/dockerizethis/internal/plan"
)

//go:embed templates/*.tmpl
var templateFS embed.FS
var templates = template.Must(template.New("java").ParseFS(templateFS, "templates/*.tmpl"))

var versionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

type renderData struct {
	plan.Plan
	BuildImage string
	JarGlob    string
	Command    string
}

// RenderJava returns a Dockerfile and .dockerignore without changing p.
// PkgManager selects the build image and jar location: maven builds on
// maven:3.9-eclipse-temurin-VERSION and packages into target/, gradle builds
// on gradle:jdkVERSION into build/libs/. Quarkus emits its runner jar under
// quarkus-app/ instead. The runtime is always eclipse-temurin:VERSION-jre.
func RenderJava(p plan.Plan) ([]emit.File, error) {
	if p.Stack != "java" {
		return nil, fmt.Errorf("render java: unsupported stack %q", p.Stack)
	}
	if !versionPattern.MatchString(p.Version) {
		return nil, fmt.Errorf("render java: version must be a numeric Java version")
	}
	switch p.Process {
	case plan.ProcessWeb:
		if p.Port < 1 || p.Port > 65535 {
			return nil, fmt.Errorf("render java: port must be between 1 and 65535")
		}
	case plan.ProcessWorker:
	default:
		return nil, fmt.Errorf("render java: unsupported process %q", p.Process)
	}
	if strings.TrimSpace(p.StartCmd) == "" || strings.ContainsAny(p.StartCmd, "\r\n\x00") {
		return nil, fmt.Errorf("render java: start command must be a nonempty single line")
	}
	if p.BuildCmd != "" && (strings.TrimSpace(p.BuildCmd) == "" || strings.ContainsAny(p.BuildCmd, "\r\n\x00") || strings.HasSuffix(strings.TrimSpace(p.BuildCmd), "\\")) {
		return nil, fmt.Errorf("render java: build command must be a nonempty single line without a trailing backslash")
	}
	d := renderData{Plan: p, Command: jsonArray([]string{"/bin/sh", "-c", "exec " + p.StartCmd})}
	switch p.PkgManager {
	case "maven":
		d.BuildImage = "maven:3.9-eclipse-temurin-" + p.Version
		d.JarGlob = "target/*.jar"
		if p.Framework == "quarkus" {
			d.JarGlob = "target/quarkus-app/quarkus-run.jar"
		}
	case "gradle":
		d.BuildImage = "gradle:jdk" + p.Version
		d.JarGlob = "build/libs/*.jar"
		if p.Framework == "quarkus" {
			d.JarGlob = "build/quarkus-app/quarkus-run.jar"
		}
	default:
		return nil, fmt.Errorf("render java: unsupported package manager %q", p.PkgManager)
	}
	var files []emit.File
	for _, artifact := range []struct{ path, template string }{{"Dockerfile", "Dockerfile.tmpl"}, {".dockerignore", "dockerignore.tmpl"}} {
		var content bytes.Buffer
		if err := templates.ExecuteTemplate(&content, artifact.template, d); err != nil {
			return nil, fmt.Errorf("render java %s: %w", artifact.path, err)
		}
		files = append(files, emit.File{Path: artifact.path, Content: content.Bytes(), Mode: 0o644})
	}
	return files, nil
}

func jsonArray(values []string) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(values)
	return strings.TrimSuffix(buf.String(), "\n")
}
