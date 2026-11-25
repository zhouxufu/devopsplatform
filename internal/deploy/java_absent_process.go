package deploy

// A missing PID receipt is not alone evidence of failure. The SSH launch has no
// queue to replay: only after its original current/artifact/lock were verified,
// a readable process scan proves that neither its JVM nor its launcher exists.
// Agent jobs use their own durable dispatch receipts and never take this path.
func managedJavaAbsentProcessProbe(root, release, proc string) string {
	return `if [ ! -e "$base/.devopsplatform.pid" ]; then proc=` + shellQuote(proc) + `; checked=0; ` +
		`test -d "$proc" || exit 31; for file in "$proc"/[0-9]*/cmdline; do ` +
		`[ "$file" != "$proc/$$/cmdline" ] || continue; [ -e "$file" ] || continue; [ -r "$file" ] || exit 31; ` +
		`if raw=$(tr '\0' '\n' < "$file" 2>/dev/null); then checked=$((checked+1)); else [ ! -e "$file" ] && continue; exit 31; fi; ` +
		`if printf '%s\n' "$raw" | grep -Fqx -- ` + shellQuote(release+"/app.jar") + `; then echo 'Original managed JVM still exists without a PID receipt'; exit 31; fi; ` +
		`if printf '%s\n' "$raw" | grep -Fq -- ` + shellQuote(release+"/service.sh") + `; then echo 'Original launcher is still active; no deployment will be replayed'; exit 31; fi; done; ` +
		`test "$checked" -gt 0 && test ! -e ` + shellQuote(root+"/.devopsplatform.pid") + ` || exit 31; ` +
		`echo 'Verified original version has no managed process or active launcher; publication failed'; exit 20; fi; `
}
