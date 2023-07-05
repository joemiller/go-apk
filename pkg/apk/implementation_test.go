// Copyright 2023 Chainguard, Inc.
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

package apk

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.alpinelinux.org/alpine/go/pkg/repository"

	apkfs "github.com/chainguard-dev/go-apk/pkg/fs"
)

const (
	testDemoKey = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAwXEJ8uVwJPODshTkf2BH
pH5fVVDppOa974+IQJsZDmGd3Ny0dcd+WwYUhNFUW3bAfc3/egaMWCaprfaHn+oS
4ddbOFgbX8JCHdru/QMAAU0aEWSMybfJGA569c38fNUF/puX6XK/y0lD2SS3YQ/a
oJ5jb5eNrQGR1HHMAd0G9WC4JeZ6WkVTkrcOw55F00aUPGEjejreXBerhTyFdabo
dSfc1TILWIYD742Lkm82UBOPsOSdSfOdsMOOkSXxhdCJuCQQ70DHkw7Epy9r+X33
ybI4r1cARcV75OviyhD8CFhAlapLKaYnRFqFxlA515e6h8i8ih/v3MSEW17cCK0b
QwIDAQAB
-----END PUBLIC KEY-----
`
)

var (
	testPkg = repository.Package{
		Name:    "alpine-baselayout",
		Version: "3.2.0-r23",
		Arch:    testArch,
	}
	testPkgFilename = fmt.Sprintf("%s-%s.apk", testPkg.Name, testPkg.Version)
)

func TestInitDB(t *testing.T) {
	src := apkfs.NewMemFS()
	apk, err := New(WithFS(src), WithIgnoreMknodErrors(ignoreMknodErrors))
	require.NoError(t, err)
	err = apk.InitDB(context.Background())
	require.NoError(t, err)
	// check all of the contents
	for _, d := range initDirectories {
		fi, err := fs.Stat(src, d.path)
		require.NoError(t, err, "error statting %s", d.path)
		require.True(t, fi.IsDir(), "expected %s to be a directory, got %v", d.path, fi.Mode())
		require.Equal(t, d.perms, fi.Mode().Perm(), "expected %s to have permissions %v, got %v", d.path, d.perms, fi.Mode().Perm())
	}
	for _, f := range initFiles {
		fi, err := fs.Stat(src, f.path)
		require.NoError(t, err, "error statting %s", f.path)
		require.True(t, fi.Mode().IsRegular(), "expected %s to be a regular file, got %v", f.path, fi.Mode())
		require.Equal(t, f.perms, fi.Mode().Perm(), "mismatched permissions for %s", f.path)
		require.GreaterOrEqual(t, fi.Size(), int64(len(f.contents)), "mismatched size for %s", f.path) // actual file can be bigger than original size
	}
	if !ignoreMknodErrors {
		for _, f := range initDeviceFiles {
			fi, err := fs.Stat(src, f.path)
			require.NoError(t, err, "error statting %s", f.path)
			require.Equal(t, fi.Mode().Type()&os.ModeCharDevice, os.ModeCharDevice, "expected %s to be a character file, got %v", f.path, fi.Mode())
			targetPerms := f.perms
			actualPerms := fi.Mode().Perm()
			require.Equal(t, targetPerms, actualPerms, "expected %s to have permissions %v, got %v", f.path, targetPerms, actualPerms)
		}
	}
}

func TestSetWorld(t *testing.T) {
	src := apkfs.NewMemFS()
	apk, err := New(WithFS(src), WithIgnoreMknodErrors(ignoreMknodErrors))
	require.NoError(t, err)
	// for initialization
	err = src.MkdirAll("etc/apk", 0o755)
	require.NoError(t, err)

	// set these packages in a random order; it should write them to world in the correct order
	packages := []string{"foo", "bar", "abc", "zulu"}
	err = apk.SetWorld(packages)
	require.NoError(t, err)

	// check all of the contents
	actual, err := src.ReadFile("etc/apk/world")
	require.NoError(t, err)

	sort.Strings(packages)
	expected := strings.Join(packages, "\n") + "\n"
	require.Equal(t, expected, string(actual), "unexpected content for etc/apk/world:\nexpected %s\nactual %s", expected, actual)
}

func TestSetRepositories(t *testing.T) {
	src := apkfs.NewMemFS()
	apk, err := New(WithFS(src), WithIgnoreMknodErrors(ignoreMknodErrors))
	require.NoError(t, err)
	// for initialization

	err = src.MkdirAll("etc/apk", 0o755)
	require.NoError(t, err)

	repos := []string{"https://dl-cdn.alpinelinux.org/alpine/v3.16/main", "https://dl-cdn.alpinelinux.org/alpine/v3.16/community"}
	err = apk.SetRepositories(repos)
	require.NoError(t, err)

	// check all of the contents
	actual, err := src.ReadFile("etc/apk/repositories")
	require.NoError(t, err)

	expected := strings.Join(repos, "\n") + "\n"
	require.Equal(t, expected, string(actual), "unexpected content for etc/apk/repositories:\nexpected %s\nactual %s", expected, actual)
}

func TestSetRepositories_Empty(t *testing.T) {
	src := apkfs.NewMemFS()
	apk, err := New(WithFS(src), WithIgnoreMknodErrors(ignoreMknodErrors))
	require.NoError(t, err)
	// for initialization

	err = src.MkdirAll("etc/apk", 0o755)
	require.NoError(t, err)

	repos := []string{}
	err = apk.SetRepositories(repos)
	require.Error(t, err)
}

func TestInitKeyring(t *testing.T) {
	src := apkfs.NewMemFS()
	a, err := New(WithFS(src), WithIgnoreMknodErrors(ignoreMknodErrors))
	require.NoError(t, err)

	dir, err := os.MkdirTemp("", "go-apk")
	require.NoError(t, err)

	keyPath := filepath.Join(dir, "alpine-devel@lists.alpinelinux.org-5e69ca50.rsa.pub")
	err = os.WriteFile(keyPath, []byte(testDemoKey), 0o644) //nolint:gosec
	require.NoError(t, err)

	// Add a local file and a remote key
	keyfiles := []string{
		keyPath, "https://alpinelinux.org/keys/alpine-devel%40lists.alpinelinux.org-4a6a0840.rsa.pub",
	}
	// ensure we send things from local
	a.SetClient(&http.Client{
		Transport: &testLocalTransport{root: testPrimaryPkgDir, basenameOnly: true},
	})

	require.NoError(t, a.InitKeyring(context.Background(), keyfiles, nil))
	// InitKeyring should have copied the local key and remote key to the right place
	fi, err := src.ReadDir(DefaultKeyRingPath)
	// should be no error reading them
	require.NoError(t, err)
	// should be 2 keys
	require.Len(t, fi, 2)

	// Add an invalid file
	keyfiles = []string{
		"/liksdjlksdjlksjlksjdl",
	}
	require.Error(t, a.InitKeyring(context.Background(), keyfiles, nil))

	// Add an invalid url
	keyfiles = []string{
		"http://sldkjflskdjflklksdlksdlkjslk.net",
	}
	require.Error(t, a.InitKeyring(context.Background(), keyfiles, nil))
}

func TestLoadSystemKeyring(t *testing.T) {
	t.Run("non-existent dir", func(t *testing.T) {
		src := apkfs.NewMemFS()
		a, err := New(WithFS(src), WithIgnoreMknodErrors(ignoreMknodErrors))
		require.NoError(t, err)

		// Read the empty dir, passing a non-existent location should err
		_, err = a.loadSystemKeyring("/non/existent/dir")
		require.Error(t, err)
	})
	t.Run("empty dir", func(t *testing.T) {
		src := apkfs.NewMemFS()
		a, err := New(WithFS(src), WithIgnoreMknodErrors(ignoreMknodErrors))
		require.NoError(t, err)

		// Read the empty dir, passing only one empty location should err
		emptyDir := "/var/test/keyring"
		err = src.MkdirAll(emptyDir, 0o755)
		require.NoError(t, err)
		_, err = a.loadSystemKeyring(emptyDir)
		require.Error(t, err)
	})
	tests := []struct {
		name  string
		paths []string
	}{
		{"non-standard dir", []string{"/var/test/keyring"}},
		{"standard dir", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arch := ArchToAPK(runtime.GOARCH)
			src := apkfs.NewMemFS()
			a, err := New(WithFS(src), WithIgnoreMknodErrors(ignoreMknodErrors))
			require.NoError(t, err)

			// Write some dummy keyfiles in a random location
			targetDir := DefaultSystemKeyRingPath
			if len(tt.paths) > 0 {
				targetDir = tt.paths[0]
			}
			// make the base directory and the arch-specific directory
			err = src.MkdirAll(targetDir, 0o755)
			require.NoError(t, err)
			err = src.MkdirAll(filepath.Join(targetDir, arch), 0o755)
			require.NoError(t, err)
			for _, h := range []string{"4a6a0840", "5243ef4b", "5261cecb", "6165ee59", "61666e3f"} {
				require.NoError(t, src.WriteFile(
					filepath.Join(targetDir, fmt.Sprintf("alpine-devel@lists.alpinelinux.org-%s.rsa.pub", h)),
					[]byte("testABC"), os.FileMode(0o644),
				))
			}

			for _, h := range []string{"4a6a0840", "5243ef4b", "5261cecb", "6165ee59", "61666e3f"} {
				err := src.WriteFile(
					filepath.Join(targetDir, arch, fmt.Sprintf("alpine-devel@lists.alpinelinux.org-%s.rsa.pub", h)),
					[]byte("testABC"), os.FileMode(0o644),
				)
				require.NoError(t, err)
			}

			// Add a readme file to ensure we dont read it
			require.NoError(t, src.WriteFile(
				filepath.Join(targetDir, "README.txt"), []byte("testABC"), os.FileMode(0o644),
			))

			// Successful read
			keyFiles, err := a.loadSystemKeyring(tt.paths...)
			require.NoError(t, err)
			require.Len(t, keyFiles, 5)
			// should not take into account extraneous files
			require.NotContains(t, keyFiles, filepath.Join(targetDir, "README.txt"))
		})
	}
}

func TestFetchPackage(t *testing.T) {
	var (
		repo          = repository.Repository{Uri: fmt.Sprintf("%s/%s", testAlpineRepos, testArch)}
		packages      = []*repository.Package{&testPkg}
		repoWithIndex = repo.WithIndex(&repository.ApkIndex{
			Packages: packages,
		})
		testEtag = "testetag"
		pkg      = repository.NewRepositoryPackage(&testPkg, repoWithIndex)
		ctx      = context.Background()
	)
	var prepLayout = func(t *testing.T, cache string) *APK {
		src := apkfs.NewMemFS()
		err := src.MkdirAll("lib/apk/db", 0o755)
		require.NoError(t, err, "unable to mkdir /lib/apk/db")

		opts := []Option{WithFS(src), WithIgnoreMknodErrors(ignoreMknodErrors)}
		if cache != "" {
			opts = append(opts, WithCache(cache))
		}
		a, err := New(opts...)
		require.NoError(t, err, "unable to create APK")
		err = a.InitDB(ctx)
		require.NoError(t, err)

		// set a client so we use local testdata instead of heading out to the Internet each time
		return a
	}
	t.Run("no cache", func(t *testing.T) {
		a := prepLayout(t, "")
		a.SetClient(&http.Client{
			Transport: &testLocalTransport{root: testPrimaryPkgDir, basenameOnly: true},
		})
		_, err := a.fetchPackage(ctx, pkg)
		require.NoErrorf(t, err, "unable to install package")
	})
	t.Run("cache miss no network", func(t *testing.T) {
		// we use a transport that always returns a 404 so we know we're not hitting the network
		// it should fail for a cache hit
		tmpDir := t.TempDir()
		a := prepLayout(t, tmpDir)
		a.SetClient(&http.Client{
			Transport: &testLocalTransport{fail: true},
		})
		_, err := a.fetchPackage(ctx, pkg)
		require.Error(t, err, "should fail when no cache and no network")
	})
	t.Run("cache miss network should fill cache", func(t *testing.T) {
		tmpDir := t.TempDir()
		a := prepLayout(t, tmpDir)
		// fill the cache
		repoDir := filepath.Join(tmpDir, url.QueryEscape(testAlpineRepos), testArch)
		err := os.MkdirAll(repoDir, 0o755)
		require.NoError(t, err, "unable to mkdir cache")

		cacheApkFile := filepath.Join(repoDir, testPkgFilename)

		a.SetClient(&http.Client{
			Transport: &testLocalTransport{root: testPrimaryPkgDir, basenameOnly: true},
		})
		_, err = a.fetchPackage(ctx, pkg)
		require.NoErrorf(t, err, "unable to install pkg")
		// check that the package file is in place
		_, err = os.Stat(cacheApkFile)
		require.NoError(t, err, "apk file not found in cache")
		// check that the contents are the same
		apk1, err := os.ReadFile(cacheApkFile)
		require.NoError(t, err, "unable to read cache apk file")
		apk2, err := os.ReadFile(filepath.Join(testPrimaryPkgDir, testPkgFilename))
		require.NoError(t, err, "unable to read previous apk file")
		require.Equal(t, apk1, apk2, "apk files do not match")
	})
	t.Run("cache hit no etag", func(t *testing.T) {
		tmpDir := t.TempDir()
		a := prepLayout(t, tmpDir)
		// fill the cache
		repoDir := filepath.Join(tmpDir, url.QueryEscape(testAlpineRepos), testArch)
		err := os.MkdirAll(repoDir, 0o755)
		require.NoError(t, err, "unable to mkdir cache")

		contents, err := os.ReadFile(filepath.Join(testPrimaryPkgDir, testPkgFilename))
		require.NoError(t, err, "unable to read apk file")
		cacheApkFile := filepath.Join(repoDir, testPkgFilename)
		err = os.WriteFile(cacheApkFile, contents, 0o644) //nolint:gosec // we're writing a test file
		require.NoError(t, err, "unable to write cache apk file")

		a.SetClient(&http.Client{
			// use a different root, so we get a different file
			Transport: &testLocalTransport{root: testAlternatePkgDir, basenameOnly: true, headers: map[string][]string{http.CanonicalHeaderKey("etag"): {testEtag}}},
		})
		_, err = a.fetchPackage(ctx, pkg)
		require.NoErrorf(t, err, "unable to install pkg")
		// check that the package file is in place
		_, err = os.Stat(cacheApkFile)
		require.NoError(t, err, "apk file not found in cache")
		// check that the contents are the same as the original
		apk1, err := os.ReadFile(cacheApkFile)
		require.NoError(t, err, "unable to read cache apk file")
		require.Equal(t, apk1, contents, "apk files do not match")
	})
	t.Run("cache hit etag match", func(t *testing.T) {
		tmpDir := t.TempDir()
		a := prepLayout(t, tmpDir)
		// fill the cache
		repoDir := filepath.Join(tmpDir, url.QueryEscape(testAlpineRepos), testArch)
		err := os.MkdirAll(repoDir, 0o755)
		require.NoError(t, err, "unable to mkdir cache")

		contents, err := os.ReadFile(filepath.Join(testPrimaryPkgDir, testPkgFilename))
		require.NoError(t, err, "unable to read apk file")
		cacheApkFile := filepath.Join(repoDir, testPkgFilename)
		err = os.WriteFile(cacheApkFile, contents, 0o644) //nolint:gosec // we're writing a test file
		require.NoError(t, err, "unable to write cache apk file")
		err = os.WriteFile(cacheApkFile+".etag", []byte(testEtag), 0o644) //nolint:gosec // we're writing a test file
		require.NoError(t, err, "unable to write etag")

		a.SetClient(&http.Client{
			// use a different root, so we get a different file
			Transport: &testLocalTransport{root: testAlternatePkgDir, basenameOnly: true, headers: map[string][]string{http.CanonicalHeaderKey("etag"): {testEtag}}},
		})
		_, err = a.fetchPackage(ctx, pkg)
		require.NoErrorf(t, err, "unable to install pkg")
		// check that the package file is in place
		_, err = os.Stat(cacheApkFile)
		require.NoError(t, err, "apk file not found in cache")
		// check that the contents are the same as the original
		apk1, err := os.ReadFile(cacheApkFile)
		require.NoError(t, err, "unable to read cache apk file")
		require.Equal(t, apk1, contents, "apk files do not match")
	})
	t.Run("cache hit etag miss", func(t *testing.T) {
		tmpDir := t.TempDir()
		a := prepLayout(t, tmpDir)
		// fill the cache
		repoDir := filepath.Join(tmpDir, url.QueryEscape(testAlpineRepos), testArch)
		err := os.MkdirAll(repoDir, 0o755)
		require.NoError(t, err, "unable to mkdir cache")

		contents, err := os.ReadFile(filepath.Join(testPrimaryPkgDir, testPkgFilename))
		require.NoError(t, err, "unable to read apk file")
		cacheApkFile := filepath.Join(repoDir, testPkgFilename)
		err = os.WriteFile(cacheApkFile, contents, 0o644) //nolint:gosec // we're writing a test file
		require.NoError(t, err, "unable to write cache apk file")
		err = os.WriteFile(cacheApkFile+".etag", []byte(testEtag), 0o644) //nolint:gosec // we're writing a test file
		require.NoError(t, err, "unable to write etag")

		a.SetClient(&http.Client{
			// use a different root, so we get a different file
			Transport: &testLocalTransport{root: testAlternatePkgDir, basenameOnly: true, headers: map[string][]string{http.CanonicalHeaderKey("etag"): {testEtag + "abcdefg"}}},
		})
		_, err = a.fetchPackage(ctx, pkg)
		require.NoErrorf(t, err, "unable to install pkg")
		// check that the package file is in place
		_, err = os.Stat(cacheApkFile)
		require.NoError(t, err, "apk file not found in cache")
		// check that the contents are the same as the original
		apk1, err := os.ReadFile(cacheApkFile)
		require.NoError(t, err, "unable to read cache apk file")
		apk2, err := os.ReadFile(filepath.Join(testAlternatePkgDir, testPkgFilename))
		require.NoError(t, err, "unable to read testdata apk file")
		require.Equal(t, apk1, apk2, "apk files do not match")
	})
}
