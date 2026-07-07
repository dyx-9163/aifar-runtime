package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type releaseConfig struct {
	App       string
	Package   string
	Version   string
	Commit    string
	BuildDate string
	DistDir   string
	Targets   []releaseTarget
	GoCommand string
	SBOMOnly  bool
}

type releaseTarget struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type releaseManifest struct {
	App       string            `json:"app"`
	Version   string            `json:"version"`
	Commit    string            `json:"commit"`
	BuildDate string            `json:"buildDate"`
	Artifacts []releaseArtifact `json:"artifacts"`
}

type releaseArtifact struct {
	Name       string `json:"name"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
	Archive    string `json:"archive"`
	SHA256     string `json:"sha256"`
	Binary     string `json:"binary"`
	BinaryHash string `json:"binarySha256"`
}

type goModule struct {
	Path    string    `json:"Path"`
	Version string    `json:"Version"`
	Main    bool      `json:"Main"`
	Replace *goModule `json:"Replace"`
}

type spdxDocument struct {
	SPDXVersion       string             `json:"spdxVersion"`
	DataLicense       string             `json:"dataLicense"`
	SPDXID            string             `json:"SPDXID"`
	Name              string             `json:"name"`
	DocumentNamespace string             `json:"documentNamespace"`
	CreationInfo      spdxCreationInfo   `json:"creationInfo"`
	Packages          []spdxPackage      `json:"packages"`
	Relationships     []spdxRelationship `json:"relationships"`
}

type spdxCreationInfo struct {
	Created  string   `json:"created"`
	Creators []string `json:"creators"`
}

type spdxPackage struct {
	Name             string            `json:"name"`
	SPDXID           string            `json:"SPDXID"`
	VersionInfo      string            `json:"versionInfo,omitempty"`
	DownloadLocation string            `json:"downloadLocation"`
	FilesAnalyzed    bool              `json:"filesAnalyzed"`
	Supplier         string            `json:"supplier"`
	ExternalRefs     []spdxExternalRef `json:"externalRefs,omitempty"`
}

type spdxExternalRef struct {
	ReferenceCategory string `json:"referenceCategory"`
	ReferenceType     string `json:"referenceType"`
	ReferenceLocator  string `json:"referenceLocator"`
}

type spdxRelationship struct {
	SPDXElementID      string `json:"spdxElementId"`
	RelationshipType   string `json:"relationshipType"`
	RelatedSPDXElement string `json:"relatedSpdxElement"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	config, err := parseFlags()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(config.DistDir, 0o755); err != nil {
		return err
	}
	sbom, err := generateSBOM(config)
	if err != nil {
		return err
	}
	if config.SBOMOnly {
		return writeFile(filepath.Join(config.DistDir, "sbom.spdx.json"), sbom, 0o644)
	}
	artifacts := []releaseArtifact{}
	for _, target := range config.Targets {
		artifact, err := buildReleaseTarget(config, target, sbom)
		if err != nil {
			return err
		}
		artifacts = append(artifacts, artifact)
	}
	sort.Slice(artifacts, func(i, j int) bool {
		return artifacts[i].Archive < artifacts[j].Archive
	})
	manifest := releaseManifest{
		App:       config.App,
		Version:   config.Version,
		Commit:    config.Commit,
		BuildDate: config.BuildDate,
		Artifacts: artifacts,
	}
	if err := writeJSONFile(filepath.Join(config.DistDir, "manifest.json"), manifest, 0o644); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(config.DistDir, "sbom.spdx.json"), sbom, 0o644); err != nil {
		return err
	}
	return writeChecksums(filepath.Join(config.DistDir, "checksums.txt"), artifacts)
}

func parseFlags() (releaseConfig, error) {
	var rawTargets string
	config := releaseConfig{}
	flag.StringVar(&config.App, "app", "aifar-runtime", "application name")
	flag.StringVar(&config.Package, "pkg", "./cmd/aifar-runtime", "main package")
	flag.StringVar(&config.Version, "version", "dev", "release version")
	flag.StringVar(&config.Commit, "commit", "unknown", "git commit")
	flag.StringVar(&config.BuildDate, "build-date", "", "RFC3339 build date")
	flag.StringVar(&config.DistDir, "dist", "dist", "distribution directory")
	flag.StringVar(&rawTargets, "targets", "linux/amd64", "space or comma separated GOOS/GOARCH targets")
	flag.StringVar(&config.GoCommand, "go", "go", "go command")
	flag.BoolVar(&config.SBOMOnly, "sbom-only", false, "only generate dist/sbom.spdx.json")
	flag.Parse()
	if strings.TrimSpace(config.BuildDate) == "" {
		config.BuildDate = time.Now().UTC().Format(time.RFC3339)
	}
	targets, err := parseTargets(rawTargets)
	if err != nil {
		return releaseConfig{}, err
	}
	config.Targets = targets
	return config, nil
}

func parseTargets(raw string) ([]releaseTarget, error) {
	fields := strings.Fields(strings.ReplaceAll(raw, ",", " "))
	if len(fields) == 0 {
		return nil, errors.New("at least one release target is required")
	}
	targets := make([]releaseTarget, 0, len(fields))
	for _, field := range fields {
		osName, arch, ok := strings.Cut(strings.TrimSpace(field), "/")
		if !ok || osName == "" || arch == "" {
			return nil, fmt.Errorf("invalid release target %q, expected GOOS/GOARCH", field)
		}
		targets = append(targets, releaseTarget{OS: osName, Arch: arch})
	}
	return targets, nil
}

func buildReleaseTarget(config releaseConfig, target releaseTarget, sbom []byte) (releaseArtifact, error) {
	releaseName := fmt.Sprintf("%s-%s-%s-%s", config.App, config.Version, target.OS, target.Arch)
	stageDir := filepath.Join(config.DistDir, releaseName)
	if err := os.RemoveAll(stageDir); err != nil {
		return releaseArtifact{}, err
	}
	if err := os.MkdirAll(filepath.Join(stageDir, "deploy", "systemd"), 0o755); err != nil {
		return releaseArtifact{}, err
	}
	if err := os.MkdirAll(filepath.Join(stageDir, "docs"), 0o755); err != nil {
		return releaseArtifact{}, err
	}
	if err := os.MkdirAll(filepath.Join(stageDir, "runtimes"), 0o755); err != nil {
		return releaseArtifact{}, err
	}
	if err := os.MkdirAll(filepath.Join(stageDir, "release"), 0o755); err != nil {
		return releaseArtifact{}, err
	}
	binaryName := binaryNameForTarget(config.App, target.OS)
	binaryPath := filepath.Join(stageDir, binaryName)
	if err := buildBinary(config, target, binaryPath); err != nil {
		return releaseArtifact{}, err
	}
	if err := copyReleaseFiles(stageDir); err != nil {
		return releaseArtifact{}, err
	}
	buildInfo := map[string]string{
		"app":       config.App,
		"version":   config.Version,
		"commit":    config.Commit,
		"buildDate": config.BuildDate,
		"goos":      target.OS,
		"goarch":    target.Arch,
	}
	if err := writeJSONFile(filepath.Join(stageDir, "release", "build.json"), buildInfo, 0o644); err != nil {
		return releaseArtifact{}, err
	}
	if err := writeFile(filepath.Join(stageDir, "release", "sbom.spdx.json"), sbom, 0o644); err != nil {
		return releaseArtifact{}, err
	}
	binaryHash, err := sha256File(binaryPath)
	if err != nil {
		return releaseArtifact{}, err
	}
	archivePath := filepath.Join(config.DistDir, archiveNameForTarget(releaseName, target.OS))
	if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		return releaseArtifact{}, err
	}
	if target.OS == "windows" {
		err = createZipArchive(config.DistDir, stageDir, archivePath)
	} else {
		err = createTarGzArchive(config.DistDir, stageDir, archivePath)
	}
	if err != nil {
		return releaseArtifact{}, err
	}
	archiveHash, err := sha256File(archivePath)
	if err != nil {
		return releaseArtifact{}, err
	}
	return releaseArtifact{
		Name:       releaseName,
		OS:         target.OS,
		Arch:       target.Arch,
		Archive:    filepath.Base(archivePath),
		SHA256:     archiveHash,
		Binary:     binaryName,
		BinaryHash: binaryHash,
	}, nil
}

func buildBinary(config releaseConfig, target releaseTarget, output string) error {
	ldflags := fmt.Sprintf("-s -w -X main.version=%s -X main.commit=%s -X main.buildDate=%s", config.Version, config.Commit, config.BuildDate)
	cmd := exec.Command(config.GoCommand, "build", "-buildvcs=false", "-trimpath", "-ldflags", ldflags, "-o", output, config.Package)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+target.OS, "GOARCH="+target.Arch)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func copyReleaseFiles(stageDir string) error {
	for _, name := range []string{"README.md", "SKILL.md"} {
		if err := copyFile(name, filepath.Join(stageDir, name), 0o644); err != nil {
			return err
		}
	}
	if err := copyDir("deploy/systemd", filepath.Join(stageDir, "deploy", "systemd")); err != nil {
		return err
	}
	if err := copyDir("runtimes", filepath.Join(stageDir, "runtimes")); err != nil {
		return err
	}
	return copyMarkdownFiles("docs", filepath.Join(stageDir, "docs"))
}

func generateSBOM(config releaseConfig) ([]byte, error) {
	cmd := exec.Command(config.GoCommand, "list", "-m", "-json", "all")
	output, err := cmd.Output()
	if err != nil {
		if exitErr := (&exec.ExitError{}); errors.As(err, &exitErr) {
			return nil, fmt.Errorf("generate module list: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	modules := []goModule{}
	for {
		var module goModule
		if err := decoder.Decode(&module); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		modules = append(modules, module)
	}
	sort.Slice(modules, func(i, j int) bool {
		return modules[i].Path < modules[j].Path
	})
	rootID := spdxIDForModule(config.App)
	doc := spdxDocument{
		SPDXVersion:       "SPDX-2.3",
		DataLicense:       "CC0-1.0",
		SPDXID:            "SPDXRef-DOCUMENT",
		Name:              config.App + "-" + config.Version,
		DocumentNamespace: fmt.Sprintf("https://aifar.local/spdx/%s/%s/%s", config.App, config.Version, config.Commit),
		CreationInfo: spdxCreationInfo{
			Created:  config.BuildDate,
			Creators: []string{"Tool: aifar-runtime release tool"},
		},
		Relationships: []spdxRelationship{{
			SPDXElementID:      "SPDXRef-DOCUMENT",
			RelationshipType:   "DESCRIBES",
			RelatedSPDXElement: rootID,
		}},
	}
	for _, module := range modules {
		name := module.Path
		version := module.Version
		if module.Main {
			name = config.App
			version = config.Version
		}
		pkg := spdxPackage{
			Name:             name,
			SPDXID:           spdxIDForModule(name),
			VersionInfo:      version,
			DownloadLocation: "NOASSERTION",
			FilesAnalyzed:    false,
			Supplier:         "NOASSERTION",
		}
		if !module.Main && module.Version != "" {
			pkg.ExternalRefs = []spdxExternalRef{{
				ReferenceCategory: "PACKAGE-MANAGER",
				ReferenceType:     "purl",
				ReferenceLocator:  purlForGoModule(module.Path, module.Version),
			}}
			doc.Relationships = append(doc.Relationships, spdxRelationship{
				SPDXElementID:      rootID,
				RelationshipType:   "DEPENDS_ON",
				RelatedSPDXElement: pkg.SPDXID,
			})
		}
		doc.Packages = append(doc.Packages, pkg)
	}
	return json.MarshalIndent(doc, "", "  ")
}

func writeChecksums(path string, artifacts []releaseArtifact) error {
	lines := make([]string, 0, len(artifacts)+3)
	for _, artifact := range artifacts {
		lines = append(lines, artifact.SHA256+"  "+artifact.Archive)
	}
	sort.Strings(lines)
	return writeFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func createTarGzArchive(baseDir, sourceDir, targetPath string) error {
	file, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipWriter := gzip.NewWriter(file)
	defer gzipWriter.Close()
	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()
	return filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(baseDir, path)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		_, err = io.Copy(tarWriter, input)
		return err
	})
}

func createZipArchive(baseDir, sourceDir, targetPath string) error {
	file, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer file.Close()
	zipWriter := zip.NewWriter(file)
	defer zipWriter.Close()
	return filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(baseDir, path)
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if entry.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}
		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		_, err = io.Copy(writer, input)
		return err
	})
}

func copyDir(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		targetPath := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}
		return copyFile(path, targetPath, 0o644)
	})
}

func copyMarkdownFiles(source, target string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if err := copyFile(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name()), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(source, target string, mode fs.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	output, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer output.Close()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	return output.Chmod(mode)
}

func writeJSONFile(path string, value any, mode fs.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFile(path, data, mode)
}

func writeFile(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func binaryNameForTarget(app, osName string) string {
	if osName == "windows" {
		return app + ".exe"
	}
	return app
}

func archiveNameForTarget(releaseName, osName string) string {
	if osName == "windows" {
		return releaseName + ".zip"
	}
	return releaseName + ".tar.gz"
}

func spdxIDForModule(name string) string {
	replacer := strings.NewReplacer("/", "-", "_", "-", ":", "-", "@", "-", "+", "-", " ", "-")
	value := replacer.Replace(name)
	value = strings.Trim(value, "-.")
	if value == "" {
		value = "package"
	}
	return "SPDXRef-" + value
}

func purlForGoModule(path, version string) string {
	escapedPath := strings.ReplaceAll(url.PathEscape(path), "%2F", "/")
	return "pkg:golang/" + escapedPath + "@" + url.QueryEscape(version)
}
