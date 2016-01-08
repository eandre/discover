package build

import (
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Env struct {
	RootDir string
	SrcDir  string
	Cmd     *exec.Cmd
}

func CreateEnv() (*Env, error) {
	dir, err := ioutil.TempDir("", "discover-build-env")
	if err != nil {
		return nil, err
	}

	src := filepath.Join(dir, "src")
	if err := os.Mkdir(src, 0777); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}

	goPath, err := exec.LookPath("go")
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}

	env := getGoEnv(dir)
	return &Env{
		RootDir: dir,
		SrcDir:  src,
		Cmd: &exec.Cmd{
			Path:   goPath,
			Env:    env,
			Args:   []string{"go"},
			Stdout: os.Stdout,
			Stderr: os.Stderr,
		},
	}, nil
}

func (env *Env) Destroy() error {
	if env.RootDir != "" {
		return os.RemoveAll(env.RootDir)
	}
	return nil
}

func getGoEnv(goPath string) []string {
	const prefix = "GOPATH="
	env := os.Environ()
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + goPath + ":" + e[len(prefix):]
			return env
		}
	}

	return append(env, prefix+goPath)
}
