/*
cpu runs shell commands on a remote system preserving the local
environment.

cpu provides a thin layer around ssh(1) that attempts to deduce
which directory on the remote the command should be run in.  When
there is an equivalent directory to the current working directory
on the remote system, the command gets executed under that:

	% cd src/gecko/
	% cpu -r buildmachine ./mach build

Sometimes it is necessary to give cpu extra instructions for which
directory to run the command under:

	% cpu -r buildmachine:~/src/gecko/ ./mach build

cpu attaches the local TTY to the remote TTY so that interactive
programs such as top(1) can also be used:

	% cpu -r buildmachine top

The connection closes when the interactive program terminates.

Used standalone, cpu does not offer many benefits over ssh(1) with
a few extra arguments.  However when combined with a bit of shell
magic to automatically set CPU_REMOTE (-r) as you cd into a directory
where you want commands to be run on a remote CPU machine, it all
becomes quite powerful:

	...
	% cd src/gecko/
	% cpu ./mach build
*/
package main // import "sny.no/cpu"

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path"
	"strconv"
	"strings"
)

/*
#include <unistd.h>
*/
import "C"

var (
	EX_USAGE     = 64
	EX_CMDNFOUND = 127
)

var (
	remote = flag.String("r", os.Getenv("CPU_REMOTE"),
		"remote compute machine, with an optional path overriding the cwd")
	shell = flag.String("s", os.Getenv("SHELL"),
		"override shell to use on remote")
	// TODO(ato): add support for passing through environ(7)
	verbose = flag.Bool("v", false, "increase verbosity")
)

func main() {
	flag.Parse()
	command := flag.Args()

	if len(*remote) == 0 {
		exit(EX_USAGE, "missing remote machine")
	}
	if len(command) == 0 {
		exit(EX_USAGE, "missing command")
	}

	login, path := splitLoginPath(*remote)
	rcpu(login, path, command)
}

// TODO(ato): this needs improvement
func makeEnvironment(environ []string) string {
	var env = make([]string, 2)
	for _, kv := range environ {
		if strings.HasPrefix(kv, "TERM=") || strings.HasPrefix(kv, "PAGER=") {
			env = append(env, kv)
		}
	}
	return strings.Join(env, " ")
}

// Attempt to reuse same shell as on the local system.
func makeShellWrapper(shell string, cmd string) string {
	switch path.Base(shell) {
	case "bash":
		return fmt.Sprintf("bash -ci %s", strconv.Quote(cmd))
	default:
		if *verbose {
			log.Println("unknown shell:", shell)
		}
		return strconv.Quote(cmd)
	}
}

// Crafts the full command to be execute on the remote.
func makeRemoteCmd(cwd string, args []string) string {
	cmd := strings.Join(args, " ")
	env := makeEnvironment(os.Environ())
	wrapper := makeShellWrapper(*shell, cmd)
	return fmt.Sprintf("{ cd %s && %s %s; }", cwd, env, wrapper)
}

func makeSshArgs(login string) []string {
	args := make([]string, 0)

	// suppress ssh(1) output when CPU_SSH_ARGS is not given
	if os.Getenv("CPU_SSH_ARGS") == "" {
		args = append(args, "-o LogLevel=QUIET")
	} else {
		sshArgs := strings.Fields(os.Getenv("CPU_SSH_ARGS"))
		args = append(args, sshArgs...)
	}

	// force pseudo-terminal allocation if any FDs are TTYs
	if isatty(os.Stdout) || isatty(os.Stdin) || isatty(os.Stderr) {
		args = append(args, "-tt")
	} else {
		args = append(args, "-e", "none", "-T")
	}

	return append(args, login)
}

func rcpu(login string, path string, args []string) {
	path = relativizeHomeDir(path)

	fullArgs := append(makeSshArgs(login), makeRemoteCmd(path, args))

	cmd := exec.Command("ssh", fullArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if *verbose {
		log.Println(cmd)
	}

	if err := cmd.Start(); err != nil {
		exit(EX_CMDNFOUND, err.Error())
	}

	if err := cmd.Wait(); err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			os.Exit(exiterr.ExitCode())
		} else {
			log.Fatalf("cmd.Wait: %v", err)
		}
	}
}

// If path begins with current user's home directory,
// replace it with ~ so home directory can be referenced across systems.
func relativizeHomeDir(path string) string {
	usr, err := user.Current()
	if err != nil {
		log.Println("user.Current():", err)
		return path
	}
	if strings.Index(path, usr.HomeDir) == 0 {
		relPath := path[len(usr.HomeDir):]
		return fmt.Sprintf("~%s", relPath)
	}
	return path
}

// [<user>@]<host>[:<path>] -> login, path
func splitLoginPath(remote string) (string, string) {
	var login, path string
	ss := strings.SplitN(remote, ":", 2)
	switch len(ss) {
	case 1:
		login = ss[0]
		path, _ = os.Getwd()
	case 2:
		login = ss[0]
		path = ss[1]
	}
	return login, path
}

func isatty(fd *os.File) bool {
	return int(C.isatty(C.int(fd.Fd()))) != 0
}

func exit(code int, format string, a ...interface{}) {
	msg := fmt.Sprintf("%s: %s\n", os.Args[0], fmt.Sprintf(format, a...))
	fmt.Fprintln(os.Stderr, msg)
	if code == EX_USAGE {
		flag.Usage()
	}
	os.Exit(code)
}
