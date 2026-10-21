package verificationplan

import "strconv"

// StepTerminationGraceSeconds bounds shutdown after a step's deadline. The
// independent run deadline may end the VM before this grace or always steps.
const StepTerminationGraceSeconds = 1

// StepCommand is shared by compiled plans and the worker's structured-step
// supervisor. Only timeout's own diagnostic channel determines whether a
// forced kill is a timeout; a customer command exiting 137 keeps that status.
// The outer job shell has already loaded the image profile. Nested step shells
// must preserve prepared PATH/venv/runtime selection instead of resetting it.
func StepCommand(command string, timeoutSeconds int) string {
	if timeoutSeconds <= 0 {
		return "    /bin/bash --noprofile --norc -c " + ShellQuoteArg(command) + "\n"
	}
	// timeout cancels its KILL escalation when its immediate child exits. Keep
	// this supervisor alive after TERM even if the customer shell exits first,
	// so TERM-resistant descendants are killed before an always step starts.
	// A caught trap (rather than ignored TERM) preserves the command's signal
	// handling; running it in the foreground also preserves its stdin.
	supervisor := `if [ "$1" = x ]; then export LC_ALL="$2"; else unset LC_ALL; fi
trap 'while :; do sleep 1; done' TERM
/bin/bash --noprofile --norc -c "$3" 2>&3
exit $?`
	return `__readyci_timeout_log="$(mktemp)" || exit 125
# Keep command stderr out of timeout's diagnostic channel and preserve its locale.
LC_ALL=C timeout --verbose --kill-after=` + strconv.Itoa(StepTerminationGraceSeconds) + `s ` + strconv.Itoa(timeoutSeconds) + `s /bin/bash -c ` + ShellQuoteArg(supervisor) + ` readyci-step "${LC_ALL+x}" "${LC_ALL-}" ` + ShellQuoteArg(command) + ` 3>&2 2>"$__readyci_timeout_log"
__readyci_step_status=$?
if [ "$__readyci_step_status" -eq 137 ] && LC_ALL=C grep -Fq 'timeout: sending signal TERM to command' "$__readyci_timeout_log"; then
  __readyci_step_status=124
fi
cat "$__readyci_timeout_log" >&2
rm -f -- "$__readyci_timeout_log"
exit "$__readyci_step_status"
`
}
