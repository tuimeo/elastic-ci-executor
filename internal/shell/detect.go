package shell

import "fmt"

// ShellDetectScript is a POSIX-compatible shell snippet that probes for the best
// available shell, mirroring GitLab Runner Docker executor's BashDetectShellScript.
// Priority: bash (preferred for full syntax support) → sh → busybox sh.
// The result is stored in the _SHELL variable.
const ShellDetectScript = `_SHELL=sh; for _s in /usr/local/bin/bash /usr/bin/bash /bin/bash /usr/local/bin/sh /usr/bin/sh /bin/sh /busybox/sh; do if [ -x "$_s" ]; then _SHELL="$_s"; break; fi; done`

// WrapScriptFile returns a shell command that detects the best shell, then uses
// it to execute the given script file. A sentinel suffix is appended to capture
// the exit code.
//
// Example output:
//
//	_SHELL=sh; for _s in ...; done; "$_SHELL" /tmp/_ci_script.sh
//	printf '%s%d\n' '__ELASTIC_CI_EXIT_123__' "$?"
func WrapScriptFile(scriptPath, sentinel string) string {
	return fmt.Sprintf(
		"%s; \"$_SHELL\" %s\nprintf '%%s%%d\\n' '%s' \"$?\"",
		ShellDetectScript, scriptPath, sentinel,
	)
}

// WrapInlineScript returns a shell command that detects the best shell, then
// uses it to execute the given script content inline via -c.
//
// Example output:
//
//	_SHELL=sh; for _s in ...; done; "$_SHELL" -c '<script>'; echo "__ELASTIC_CI_EXIT_123__$?"
func WrapInlineScript(quotedScript, sentinel string) string {
	return fmt.Sprintf(
		"%s; \"$_SHELL\" -c %s; echo \"%s$?\"",
		ShellDetectScript, quotedScript, sentinel,
	)
}
