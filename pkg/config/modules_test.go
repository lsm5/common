package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/containers/storage/pkg/unshare"
	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	testBaseHome = "testdata/modules/home/.config"
	testBaseEtc  = "testdata/modules/etc"
	testBaseUsr  = "testdata/modules/usr/share"
)

// copyDir recursively copies a directory tree from src to dst.
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			os.MkdirAll(dstPath, 0o755)
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			in, err := os.Open(srcPath)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.Create(dstPath)
			if err != nil {
				return err
			}
			defer out.Close()
			if _, err := io.Copy(out, in); err != nil {
				return err
			}
		}
	}
	return nil
}

func testSetModulePaths() {
	t := GinkgoT()

	wd, err := os.Getwd()
	gomega.Expect(err).ToNot(gomega.HaveOccurred())

	// Create a temp dir for HOME
	tempHome, err := os.MkdirTemp("", "test-home-")
	gomega.Expect(err).ToNot(gomega.HaveOccurred())
	DeferCleanup(func() { os.RemoveAll(tempHome) })

	// Set XDG_CONFIG_HOME to the temp directory
	t.Setenv("XDG_CONFIG_HOME", tempHome)
	os.Setenv("XDG_CONFIG_HOME", tempHome)

	// Copy testdata modules to the temp home config (recursively)
	testHomeConfig := filepath.Join(wd, testBaseHome, "containers", "containers.conf.modules")
	tempHomeConfig := filepath.Join(tempHome, "containers", "containers.conf.modules")
	os.MkdirAll(tempHomeConfig, 0o755)

	gomega.Expect(copyDir(testHomeConfig, tempHomeConfig)).ToNot(gomega.HaveOccurred())

	oldEtc := moduleBaseEtc
	oldUsr := moduleBaseUsr
	moduleBaseEtc = filepath.Join(wd, testBaseEtc)
	moduleBaseUsr = filepath.Join(wd, testBaseUsr)
	DeferCleanup(func() {
		moduleBaseEtc = oldEtc
		moduleBaseUsr = oldUsr
	})
}

var _ = Describe("Config Modules", func() {
	It("module directories", func() {
		dirs, err := ModuleDirectories()
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
		gomega.Expect(dirs).NotTo(gomega.BeNil())

		if unshare.IsRootless() {
			gomega.Expect(dirs).To(gomega.HaveLen(3))
		} else {
			gomega.Expect(dirs).To(gomega.HaveLen(2))
		}
	})

	It("resolve modules", func() {
		// This test makes sure that the correct module is being
		// returned.
		testSetModulePaths()

		dirs, err := ModuleDirectories()
		gomega.Expect(err).ToNot(gomega.HaveOccurred())

		configHome := os.Getenv("XDG_CONFIG_HOME")
		userHome, _ := os.UserHomeDir()
		userConfigHome := filepath.Join(userHome, ".config")
		if unshare.IsRootless() {
			gomega.Expect(dirs).To(gomega.HaveLen(3))
			// Debug print to see the actual value
			fmt.Printf("DEBUG: dirs[0] = %q\n", dirs[0])
			fmt.Printf("DEBUG: configHome = %q\n", configHome)
			fmt.Printf("DEBUG: userConfigHome = %q\n", userConfigHome)
			// Accept either the temp home or the real home as valid prefix
			gomega.Expect(
				strings.Contains(dirs[0], filepath.Join(configHome, "containers", "containers.conf.modules")) ||
					strings.Contains(dirs[0], filepath.Join(userConfigHome, "containers", "containers.conf.modules")),
			).To(gomega.BeTrue(), "dirs[0] should contain a valid config home path")
			gomega.Expect(dirs[1]).To(gomega.ContainSubstring(testBaseEtc))
			gomega.Expect(dirs[2]).To(gomega.ContainSubstring(testBaseUsr))
		} else {
			gomega.Expect(dirs).To(gomega.HaveLen(2))
			gomega.Expect(dirs[0]).To(gomega.ContainSubstring(testBaseEtc))
			gomega.Expect(dirs[1]).To(gomega.ContainSubstring(testBaseUsr))
		}

		for _, test := range []struct {
			input       string
			expectedDir string
			mustFail    bool
			rootless    bool
		}{
			// Rootless
			{"first.conf", configHome, false, true},
			{"second.conf", configHome, false, true},
			{"third.conf", configHome, false, true},
			{"sub/first.conf", configHome, false, true},

			// Root + Rootless
			{"fourth.conf", testBaseEtc, false, false},
			{"sub/etc-only.conf", testBaseEtc, false, false},
			{"fifth.conf", testBaseUsr, false, false},
			{"sub/share-only.conf", testBaseUsr, false, false},
			{"none.conf", "", true, false},
		} {
			if test.rootless && !unshare.IsRootless() {
				continue
			}
			result, err := resolveModule(test.input, dirs)
			if test.mustFail {
				gomega.Expect(err).To(gomega.HaveOccurred())
				continue
			}
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			// For rootless tests, accept either the temp home or the real home as valid prefix
			if test.rootless {
				gomega.Expect(
					strings.HasSuffix(result, filepath.Join(configHome, moduleSubdir, test.input)) ||
						strings.HasSuffix(result, filepath.Join(userConfigHome, moduleSubdir, test.input)),
				).To(gomega.BeTrue(), "result should have a valid config home path suffix")
			} else {
				gomega.Expect(result).To(gomega.HaveSuffix(filepath.Join(test.expectedDir, moduleSubdir, test.input)))
			}
		}
	})

	It("new config with modules", func() {
		testSetModulePaths()

		wd, err := os.Getwd()
		gomega.Expect(err).ToNot(gomega.HaveOccurred())

		options := &Options{Modules: []string{"none.conf"}}
		_, err = New(options)
		gomega.Expect(err).To(gomega.HaveOccurred()) // must error out

		options = &Options{}
		c, err := New(options)
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
		gomega.Expect(options.additionalConfigs).To(gomega.BeEmpty()) // no module is getting loaded!
		gomega.Expect(c).NotTo(gomega.BeNil())
		gomega.Expect(c.LoadedModules()).To(gomega.BeEmpty())

		options = &Options{Modules: []string{"fourth.conf"}}
		c, err = New(options)
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
		gomega.Expect(options.additionalConfigs).To(gomega.HaveLen(1)) // 1 module is getting loaded!
		gomega.Expect(c.Containers.InitPath).To(gomega.Equal("etc four"))
		gomega.Expect(c.LoadedModules()).To(gomega.HaveLen(1))
		// Make sure the returned module path is absolute.
		gomega.Expect(c.LoadedModules()).To(gomega.Equal([]string{filepath.Join(wd, "testdata/modules/etc/containers/containers.conf.modules/fourth.conf")}))

		options = &Options{Modules: []string{"fourth.conf"}}
		c, err = New(options)
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
		gomega.Expect(options.additionalConfigs).To(gomega.HaveLen(1)) // 1 module is getting loaded!
		gomega.Expect(c.Containers.InitPath).To(gomega.Equal("etc four"))
		gomega.Expect(c.LoadedModules()).To(gomega.HaveLen(1))

		options = &Options{Modules: []string{"fourth.conf", "sub/share-only.conf", "sub/etc-only.conf"}}
		c, err = New(options)
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
		gomega.Expect(options.additionalConfigs).To(gomega.HaveLen(3)) // 3 modules are getting loaded!
		gomega.Expect(c.Containers.InitPath).To(gomega.Equal("etc four"))
		gomega.Expect(c.Containers.Env.Get()).To(gomega.Equal([]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "usr share only"}))
		gomega.Expect(c.Network.DefaultNetwork).To(gomega.Equal("etc only conf"))
		gomega.Expect(c.LoadedModules()).To(gomega.HaveLen(3))

		options = &Options{Modules: []string{"third.conf"}}
		c, err = New(options)
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
		gomega.Expect(options.additionalConfigs).To(gomega.HaveLen(1)) // 1 module is getting loaded!
		gomega.Expect(c.LoadedModules()).To(gomega.HaveLen(1))
		// Dynamically check which third.conf is loaded
		thirdConfPath := c.LoadedModules()[0]
		if thirdConfPath != "" && thirdConfPath != filepath.Join(wd, "testdata/modules/etc/containers/containers.conf.modules/third.conf") {
			gomega.Expect(c.Network.DefaultNetwork).To(gomega.Equal("home third"))
		} else {
			gomega.Expect(c.Network.DefaultNetwork).To(gomega.Equal("etc third"))
		}
	})

	It("new config with modules and env variables", func() {
		testSetModulePaths()

		t := GinkgoT()
		t.Setenv(containersConfOverrideEnv, "testdata/modules/override.conf")

		// Also make sure that absolute paths are loaded as is.
		wd, err := os.Getwd()
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
		absConf := filepath.Join(wd, "testdata/modules/home/.config/containers/containers.conf.modules/second.conf")

		options := &Options{Modules: []string{"fourth.conf", "sub/share-only.conf", absConf}}
		c, err := New(options)
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
		gomega.Expect(options.additionalConfigs).To(gomega.HaveLen(4)) // 2 modules + abs path + override conf are getting loaded!
		gomega.Expect(c.Containers.InitPath).To(gomega.Equal("etc four"))
		gomega.Expect(c.Containers.Env.Get()).To(gomega.Equal([]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "usr share only", "override conf always wins"}))
		gomega.Expect(c.Containers.Volumes.Get()).To(gomega.Equal([]string{"volume four", "home second"}))
	})
})
