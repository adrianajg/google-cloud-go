// Copyright 2022 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"cloud.google.com/go/internal/gapicgen/generator"
	"cloud.google.com/go/internal/gapicgen/git"
	"cloud.google.com/go/internal/gensnippets"
	"cloud.google.com/go/internal/postprocessor/execv"
	"cloud.google.com/go/internal/postprocessor/execv/gocmd"
	"github.com/google/go-github/github"
	"golang.org/x/sync/errgroup"

	// "github.com/google/go-github/v35/github"
	"gopkg.in/yaml.v2"
)

func main() {
	var stagingDir string
	var clientRoot string
	var googleapisDir string
	var directories string
	flag.StringVar(&stagingDir, "stage-dir", "owl-bot-staging/src/", "Path to owl-bot-staging directory")
	flag.StringVar(&clientRoot, "client-root", "/repo", "Path to clients")
	flag.StringVar(&googleapisDir, "googleapis-dir", "", "Path to googleapis/googleapis repo")
	// The module names are relative to the client root - do not add paths. See README for example.
	flag.StringVar(&directories, "dirs", "", "Comma-separated list of modules to run")

	branchPrefix := flag.String("branch", "owl-bot-copy-", "The prefix of the branch that OwlBot opens when working on a PR.")
	githubAccessToken := flag.String("githubAccessToken", os.Getenv("GITHUB_ACCESS_TOKEN"), "The token used to open pull requests.")
	githubUsername := flag.String("githubUsername", os.Getenv("GITHUB_USERNAME"), "The GitHub user name for the author.")
	githubName := flag.String("githubName", os.Getenv("GITHUB_NAME"), "The name of the author for git commits.")
	githubEmail := flag.String("githubEmail", os.Getenv("GITHUB_EMAIL"), "The email address of the author.")

	flag.Parse()

	ctx := context.Background()

	log.Println("stage-dir set to", stagingDir)
	log.Println("client-root set to", clientRoot)
	log.Println("googleapis-dir set to", googleapisDir)

	cc := &clientConfig{
		githubAccessToken: *githubAccessToken,
		githubUsername:    *githubUsername,
		githubName:        *githubName,
		githubEmail:       *githubEmail,
		branchPrefix:      *branchPrefix,
	}

	log.Println("clientConfig instance is", *cc)

	var modules []string
	if directories != "" {
		dirSlice := strings.Split(directories, ",")
		for _, dir := range dirSlice {
			modules = append(modules, filepath.Join(clientRoot, dir))
		}
	}

	log.Println("modules set to", modules)
	// var genprotoDir string
	if googleapisDir == "" {
		log.Println("creating temp dir")
		tmpDir, err := ioutil.TempDir("", "update-postprocessor")
		if err != nil {
			log.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		log.Printf("working out %s\n", tmpDir)
		googleapisDir = filepath.Join(tmpDir, "googleapis")
		// genprotoDir = filepath.Join(tmpDir, "googleapis-gen")

		// Clone repository for use in parsing API shortnames.
		// TODO: if not cloning other repos clean up
		grp, _ := errgroup.WithContext(ctx)
		grp.Go(func() error {
			return git.DeepClone("https://github.com/googleapis/googleapis", googleapisDir)
		})
		// attempting to clone this results in an error: "authentication required"
		// grp.Go(func() error {
		// 	return git.DeepClone("https://github.com/googleapis/googleapis-gen", genprotoDir)
		// })

		if err := grp.Wait(); err != nil {
			log.Fatal(err)
		}
	}

	c := &config{
		googleapisDir:  googleapisDir,
		googleCloudDir: clientRoot,
		// genprotoDir:    genprotoDir,
		stagingDir: stagingDir,
		modules:    modules,
	}

	if err := c.run(ctx, cc); err != nil {
		log.Fatal(err)
	}

	// TODO: delete owl-bot-staging file
	log.Println("End of postprocessor script.")
}

type config struct {
	googleapisDir  string
	googleCloudDir string
	// genprotoDir    string
	stagingDir string
	modules    []string
}

type clientConfig struct {
	githubAccessToken string
	githubUsername    string
	githubName        string
	githubEmail       string
	branchPrefix      string
}

func (c *config) run(ctx context.Context, cc *clientConfig) error {
	if err := c.amendPRDescription(ctx, cc); err != nil {
		return err
	}

	// filepath.WalkDir(c.stagingDir, func(path string, d fs.DirEntry, err error) error {
	// 	if err != nil {
	// 		return err
	// 	}
	// 	if d.IsDir() {
	// 		return nil
	// 	}
	// 	dstPath := filepath.Join(c.googleCloudDir, strings.TrimPrefix(path, c.stagingDir))
	// 	if err := copyFiles(path, dstPath); err != nil {
	// 		return err
	// 	}
	// 	return nil
	// })
	// if err := gocmd.ModTidyAll(c.googleCloudDir); err != nil {
	// 	return err
	// }
	// if err := gocmd.Vet(c.googleCloudDir); err != nil {
	// 	return err
	// }
	// if err := c.regenSnippets(); err != nil {
	// 	return err
	// }
	// if _, err := c.manifest(generator.MicrogenGapicConfigs); err != nil {
	// 	return err
	// }
	return nil
}

func copyFiles(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	if err != nil {
		return err
	}

	return nil
}

// RegenSnippets regenerates the snippets for all GAPICs configured to be generated.
func (c *config) regenSnippets() error {
	log.Println("regenerating snippets")

	snippetDir := filepath.Join(c.googleCloudDir, "internal", "generated", "snippets")
	apiShortnames, err := generator.ParseAPIShortnames(c.googleapisDir, generator.MicrogenGapicConfigs, generator.ManualEntries)

	if err != nil {
		return err
	}
	if err := gensnippets.GenerateSnippetsDirs(c.googleCloudDir, snippetDir, apiShortnames, c.modules); err != nil {
		log.Printf("warning: got the following non-fatal errors generating snippets: %v", err)
	}
	if err := c.replaceAllForSnippets(c.googleCloudDir, snippetDir); err != nil {
		return err
	}
	if err := gocmd.ModTidy(snippetDir); err != nil {
		return err
	}

	return nil
}

func (c *config) replaceAllForSnippets(googleCloudDir, snippetDir string) error {
	return execv.ForEachMod(googleCloudDir, func(dir string) error {
		if c.modules != nil {
			for _, mod := range c.modules {
				if !strings.Contains(dir, mod) {
					return nil
				}
			}
		}
		if dir == snippetDir {
			return nil
		}
		mod, err := gocmd.ListModName(dir)
		if err != nil {
			return err
		}
		// Replace it. Use a relative path to avoid issues on different systems.
		rel, err := filepath.Rel(snippetDir, dir)
		if err != nil {
			return err
		}
		c := execv.Command("bash", "-c", `go mod edit -replace "$MODULE=$MODULE_PATH"`)
		c.Dir = snippetDir
		c.Env = []string{
			fmt.Sprintf("PATH=%s", os.Getenv("PATH")), // TODO(deklerk): Why do we need to do this? Doesn't seem to be necessary in other exec.Commands.
			fmt.Sprintf("HOME=%s", os.Getenv("HOME")), // TODO(deklerk): Why do we need to do this? Doesn't seem to be necessary in other exec.Commands.
			fmt.Sprintf("MODULE=%s", mod),
			fmt.Sprintf("MODULE_PATH=%s", rel),
		}
		return c.Run()
	})
}

// manifest writes a manifest file with info about all of the confs.
func (c *config) manifest(confs []*generator.MicrogenConfig) (map[string]generator.ManifestEntry, error) {
	log.Println("updating gapic manifest")
	entries := map[string]generator.ManifestEntry{} // Key is the package name.
	f, err := os.Create(filepath.Join(c.googleCloudDir, "internal", ".repo-metadata-full.json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	for _, manual := range generator.ManualEntries {
		entries[manual.DistributionName] = manual
	}
	for _, conf := range confs {
		yamlPath := filepath.Join(c.googleapisDir, conf.InputDirectoryPath, conf.ApiServiceConfigPath)
		yamlFile, err := os.Open(yamlPath)
		if err != nil {
			return nil, err
		}
		yamlConfig := struct {
			Title string `yaml:"title"` // We only need the title field.
		}{}
		if err := yaml.NewDecoder(yamlFile).Decode(&yamlConfig); err != nil {
			return nil, fmt.Errorf("decode: %v", err)
		}
		docURL, err := docURL(c.googleCloudDir, conf.ImportPath)
		if err != nil {
			return nil, fmt.Errorf("unable to build docs URL: %v", err)
		}
		entry := generator.ManifestEntry{
			DistributionName:  conf.ImportPath,
			Description:       yamlConfig.Title,
			Language:          "Go",
			ClientLibraryType: "generated",
			DocsURL:           docURL,
			ReleaseLevel:      conf.ReleaseLevel,
			LibraryType:       generator.GapicAutoLibraryType,
		}
		entries[conf.ImportPath] = entry
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return entries, enc.Encode(entries)
}

func docURL(cloudDir, importPath string) (string, error) {
	suffix := strings.TrimPrefix(importPath, "cloud.google.com/go/")
	mod, err := gocmd.CurrentMod(filepath.Join(cloudDir, suffix))
	if err != nil {
		return "", err
	}
	pkgPath := strings.TrimPrefix(strings.TrimPrefix(importPath, mod), "/")
	return "https://cloud.google.com/go/docs/reference/" + mod + "/latest/" + pkgPath, nil
}

// branch name assigned by OwlBot for mono repos is 'owl-bot-copy'
// (https://github.com/googleapis/repo-automation-bots/blob/57f0cabf9379ba41df0a1894f153236022ada38b/packages/owl-bot/src/copy-code.ts#L247)
// var OWL_BOT_BRANCH_NAME string = "owl-bot-copy"
var OWL_BOT_BRANCH_NAME string = "CommitMessages"

// for testing run `$ go run main.go -googleapis-dir="/home/guadriana/developer/googleapis" -branch="CommitMessages"`
func (c *config) amendPRDescription(ctx context.Context, cc *clientConfig) error {
	PR, err := getPR(ctx, cc)
	if err != nil {
		return err
	}

	PRTitle := PR.Title
	PRBody := PR.Body
	log.Println("PRTitle is", *PRTitle)
	// log.Println("PRBody is", *PRBody)

	newPRTitle, _, err := processCommit(*PRTitle, *PRBody, c.googleapisDir)
	if err != nil {
		return err
	}
	log.Println("newPRTitle is", newPRTitle)
	// log.Println("split PRBody is", newPRBody)

	// // functioning example commit hashes:922f1f33bb239addc9816fbbecbf15376e03a4aa, cb6fbe8784479b22af38c09a5039d8983e894566
	// // returns empty slice: c40ef67da867b3bdba3d1876b2aa17791c4971d0, 2a470e2797e45c8afd388d672cb759b14115b2fc
	// // commit hashes that do not work with error "bad object <hash>": b86d2742eeef819831e0fd2948d0fe931b2be80c
	// // this sample hash returns a scope of "scanner"
	// scopesSlice, err := getScopeFromGoogleapisCommitHash("922f1f33bb239addc9816fbbecbf15376e03a4aa", c.googleapisDir)
	// if err != nil {
	// 	return err
	// }
	// log.Println("scopes are", scopesSlice)
	//
	// var scope string
	// if len(scopesSlice) == 1 {
	// 	scope = scopesSlice[0]
	// }
	// *PRTitle = addScopeToCommitTitle(*PRTitle, scope, c.googleapisDir)
	// log.Println("PRTitle with scope added is", *PRTitle)

	return nil
}

// given a PR number,
func getPR(ctx context.Context, cc *clientConfig) (*github.PullRequest, error) {
	client := github.NewClient(nil)

	PRs, _, err := client.PullRequests.List(ctx, cc.githubUsername, "google-cloud-go", nil)
	if err != nil {
		return nil, err
	}
	// How to ensure this is the PR opened by OwlBot?
	PR, err := findValidPR(ctx, cc, PRs)
	if err != nil {
		return nil, err
	}

	return PR, nil
}

func findValidPR(ctx context.Context, cc *clientConfig, PRs []*github.PullRequest) (*github.PullRequest, error) {
	var PR *github.PullRequest
	for _, thisPR := range PRs {
		if strings.Contains(*thisPR.Head.Label, cc.branchPrefix) {
			PR = thisPR
			return PR, nil
		}
	}
	return nil, errors.New("no PR found")
}

func (c *config) getChangedPackageNames() []string {
	moduleNamesMap := make(map[string]bool)
	var moduleNames []string
	filepath.WalkDir(c.stagingDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Println(err)
		}
		if d.IsDir() {
			modulePath := strings.TrimPrefix(path, c.stagingDir+"/")
			modulePathParts := strings.Split(modulePath, "/")
			moduleName := modulePathParts[0]
			if _, value := moduleNamesMap[moduleName]; !value {
				moduleNamesMap[moduleName] = true
				if moduleName != ".." {
					moduleNames = append(moduleNames, moduleName)
				}
			}
		}
		return nil
	})
	return moduleNames
}

func getScopeFromGoogleapisCommitHash(commitHash, googleapisDir string) ([]string, error) {
	c := execv.Command("git", "diff-tree", "--no-commit-id", "--name-only", "-r", commitHash)
	c.Dir = googleapisDir
	b, err := c.Output()
	if err != nil {
		return nil, err
	}
	files := string(b)
	files = filepath.Dir(files)
	log.Println("files are", files)
	filesSlice := strings.Split(string(files), "\n")

	scopesMap := make(map[string]bool)
	var scopes []string
	for _, filePath := range filesSlice {
		for _, config := range generator.MicrogenGapicConfigs {
			if config.InputDirectoryPath == filePath {
				scope := config.Pkg
				if _, value := scopesMap[scope]; !value {
					scopesMap[scope] = true
					scopes = append(scopes, scope)
				}
				break
			}
		}
	}
	return scopes, nil
}

func addScopeToCommitTitle(title, scope, googleapisDir string) string {
	// from FormatChanges func
	if scope != "" {
		// Try to add in pkg affected into conventional commit scope.
		titleParts := strings.SplitN(title, ":", 2)
		if len(titleParts) == 2 {
			// If a scope is already provided, remove it.
			if i := strings.Index(titleParts[0], "("); i > 0 {
				titleParts[0] = titleParts[0][:i]
			}

			var breakChangeIndicator string
			if strings.HasSuffix(titleParts[0], "!") {
				// If the change is marked as breaking we need to move the
				// bang to after the added scope.
				titleParts[0] = titleParts[0][:len(titleParts[0])-1]
				breakChangeIndicator = "!"
			}
			titleParts[0] = fmt.Sprintf("%s(%s)%s", titleParts[0], scope, breakChangeIndicator)
		}
		title = strings.Join(titleParts, ":")
	}
	return title
}

func processCommit(title, body, googleapisDir string) (string, string, error) {
	// var hash string
	bodySlice := strings.Split(body, "\n")
	log.Println("bodySlice[0] is", bodySlice[1])
	// var sb strings.Builder

	var titlePkg string
	var pkgSlice []string
	
	// get first scope
	for _, line := range bodySlice {
		if strings.Contains(line, "googleapis/googleapis/") {
			log.Println("line is", line)
			sourceLinkSlice := strings.SplitN(line, ": ", 2)
			log.Println("sourceLinkSlice is", sourceLinkSlice)
			hash := filepath.Base(sourceLinkSlice[1])
			log.Println("hash is", hash)
			var err error
			pkgSlice, err = getScopeFromGoogleapisCommitHash(hash, googleapisDir)
			log.Println("pkgSlice is", pkgSlice)
			if err != nil {
				return "", "", err
			}
			break
		}
	}
	if len(pkgSlice) == 1 {
		titlePkg = pkgSlice[0]
		log.Println("titlePkg is", titlePkg)
	}

	var newTitle string
	firstTitlePart := strings.SplitN(title, "[", 2)[0]
	secondTitlePart := strings.SplitN(title, "] ", 2)[1]
	secondTitlePart = strings.TrimSpace(secondTitlePart)
	firstTitlePart = strings.SplitN(firstTitlePart, ":", 2)[0]

	var breakChangeIndicator string
	if strings.HasSuffix(firstTitlePart, "!") {
		breakChangeIndicator = "!"
	}

	newTitle = fmt.Sprintf("%v(%v)%v: %v", firstTitlePart, titlePkg, breakChangeIndicator, secondTitlePart)
	log.Println("newTitle is", newTitle)

	// for _, line := range lines {

	// 		if strings.Contains(line, "[REPLACEME]") {
	// 		newTitle = line
	// 		titleSlice = strings.SplitN(newTitle, "[REPLACEME]", 2)
	// 		log.Println("titleSlice is", titleSlice)
	// 		newTitle = titleSlice[0]
	// 	}
	// }

	return newTitle, body, nil
}