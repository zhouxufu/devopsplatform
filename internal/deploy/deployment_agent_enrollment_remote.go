package deploy

import (
	"crypto/sha256"
	"fmt"
)

func renderEnrollmentLauncher(p enrollmentPlan) string {
	hash := sha256.Sum256([]byte(p.Scope.StateDir))
	return fmt.Sprintf(`#!/bin/bash -p
# devopsplatform-managed deployment worker; fixed user and executable, no free command.
set -euo pipefail
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
[[ $# == 1 ]] || exit 2
state=%s
name=$1
if [[ "$name" =~ ^check-([a-f0-9]{32})$ ]]; then
 name=${BASH_REMATCH[1]}
 dir="$state/.checks/$name"
 flag=-deployment-worker-check
elif [[ "$name" =~ ^deploy-[1-9][0-9]*$ ]]; then
 dir="$state/$name"
 flag=-deployment-worker
else exit 2; fi
[[ -d "$dir" && ! -L "$dir" && "$(readlink -f "$dir")" == "$dir" ]] || exit 2
[[ $(id -u) == 0 ]] || exit 2
exec /bin/systemd-run --service-type=simple --unit="devops-deploy-%x-$name.service" --property=KillMode=process --uid=%s --gid=%s /usr/local/bin/logagent "$flag" "$dir"
`, shellQuote(p.Scope.StateDir), hash[:6], shellQuote(p.RunUser), shellQuote(p.RunGroup))
}

func renderEnrollmentSudoers(p enrollmentPlan) string {
	if p.RunUser == "root" {
		return "# devopsplatform-managed; root requires no delegated privilege\n"
	}
	return fmt.Sprintf("# devopsplatform-managed fixed deployment worker only\nDefaults:%s !requiretty\n%s ALL=(root) NOPASSWD: /usr/local/libexec/logagent-deployment-worker *\n", p.RunUser, p.RunUser)
}

func renderEnrollmentApply(p enrollmentPlan, stage string) string {
	q := shellQuote
	return fmt.Sprintf(`set -euo pipefail
SUDO=""
if [ "$(id -u)" -ne 0 ]; then SUDO="sudo -n"; fi
stage=%s
work=%s
user=%s
group=%s
cleanup() { $SUDO rm -rf -- "$stage"; }
trap cleanup EXIT
for dir in /usr/local/bin /usr/local/libexec /etc/logagent /etc/systemd/system /etc/sudoers.d /var/lib/devops-agent-enrollment; do
 if $SUDO test -e "$dir"; then
  test "$(readlink -f "$dir")" = "$dir"
  test "$($SUDO stat -c %%u "$dir")" = 0
  mode=$($SUDO stat -c %%a "$dir")
  test $((0$mode & 0022)) -eq 0
 fi
done
$SUDO test ! -L /usr/local/bin/logagent
$SUDO test ! -L /etc/logagent/agent.yaml
$SUDO test ! -L /etc/systemd/system/logagent.service
%s
# Refuse taking over an unknown privileged helper or sudoers file.
for file in /usr/local/libexec/logagent-deployment-worker /etc/sudoers.d/logagent-deployment-worker; do
 if $SUDO test -e "$file"; then $SUDO test ! -L "$file"; $SUDO grep -q 'devopsplatform-managed' "$file"; fi
done
if ! id "$user" >/dev/null 2>&1; then
 [ "$user" = logagent ] || { echo 'Execution user missing'; exit 2; }
 getent group "$group" >/dev/null 2>&1 || $SUDO groupadd --system "$group"
 $SUDO useradd --system --gid "$group" --home-dir /var/lib/logagent --shell /usr/sbin/nologin "$user"
fi
getent group "$group" >/dev/null
# Never chown an existing queue or deployment/application tree.
if [ ! -d /var/lib/logagent ]; then $SUDO install -d -m 0755 -o "$user" -g "$group" /var/lib/logagent; fi
$SUDO test ! -L /var/lib/logagent
$SUDO install -d -m 0755 -o root -g root /var/lib/devops-agent-enrollment
$SUDO mkdir "$work"
$SUDO chown root:"$group" "$work"
$SUDO chmod 0750 "$work"
$SUDO install -d -m 0700 -o "$user" -g "$group" "$work/result"
$SUDO install -m 0755 "$stage/logagent" "$work/logagent"
$SUDO install -m 0600 -o "$user" -g "$group" "$stage/request.json" "$work/request.json"
# The existing unit's execution identity/config cannot be silently changed.
if [ -f /etc/systemd/system/logagent.service ]; then
 $SUDO grep -Fxq 'ExecStart=/usr/local/bin/logagent -config /etc/logagent/agent.yaml' /etc/systemd/system/logagent.service
fi
%s
if [ "$user" = root ]; then
 $SUDO "$work/logagent" -config /etc/logagent/agent.yaml -prepare-deployment-config "$work/request.json"
else
 $SUDO runuser -u "$user" -- "$work/logagent" -config /etc/logagent/agent.yaml -prepare-deployment-config "$work/request.json"
fi
backup=/var/lib/logagent/backups/deployment-%s
$SUDO install -d -m 0700 "$backup"
for file in /usr/local/bin/logagent /etc/logagent/agent.yaml /etc/systemd/system/logagent.service /usr/local/libexec/logagent-deployment-worker /etc/sudoers.d/logagent-deployment-worker; do
 if $SUDO test -f "$file"; then $SUDO cp -p "$file" "$backup/$(basename "$file")"; fi
done
$SUDO install -d -m 0755 /etc/logagent /usr/local/libexec
$SUDO install -m 0755 "$stage/logagent" /usr/local/bin/.logagent.enrollment
$SUDO mv /usr/local/bin/.logagent.enrollment /usr/local/bin/logagent
$SUDO install -m 0755 -o root -g root "$stage/worker" /usr/local/libexec/logagent-deployment-worker
if [ "$user" != root ]; then
 command -v visudo >/dev/null
 $SUDO visudo -cf "$stage/sudoers" >/dev/null
 $SUDO install -m 0440 -o root -g root "$stage/sudoers" /etc/sudoers.d/logagent-deployment-worker
fi
# Read the user-produced candidate as that user, never follow a source link
# with root privileges. Seal a root-owned copy before checking and installing.
if [ "$user" = root ]; then
 $SUDO cat "$work/result/agent.yaml" | $SUDO tee "$stage/agent.yaml" >/dev/null
else
 $SUDO runuser -u "$user" -- cat "$work/result/agent.yaml" | $SUDO tee "$stage/agent.yaml" >/dev/null
fi
$SUDO install -m 0640 -o root -g "$group" "$stage/agent.yaml" "$work/verified.yaml"
# Probe with the sealed candidate before switching the installed file.
if [ "$user" = root ]; then
 $SUDO /usr/local/bin/logagent -config "$work/verified.yaml" -check-deployment-worker
else
 $SUDO runuser -u "$user" -- /usr/local/bin/logagent -config "$work/verified.yaml" -check-deployment-worker
fi
$SUDO install -m 0600 -o "$user" -g "$group" "$stage/agent.yaml" /etc/logagent/.agent.enrollment
$SUDO mv /etc/logagent/.agent.enrollment /etc/logagent/agent.yaml
if [ ! -f /etc/systemd/system/logagent.service ]; then $SUDO install -m 0644 "$stage/unit" /etc/systemd/system/logagent.service; fi
$SUDO systemctl daemon-reload
$SUDO systemctl enable logagent >/dev/null
$SUDO systemctl restart logagent
sleep 1
$SUDO systemctl is-active --quiet logagent
$SUDO rm -f "$work/request.json" "$work/result/agent.yaml" "$work/verified.yaml" "$work/logagent"
`, q(stage), q("/var/lib/devops-agent-enrollment/"+p.Scope.OperationID), q(p.RunUser), q(p.RunGroup), enrollmentSourceChecks(p), enrollmentRootChecks(p), p.Scope.OperationID)
}

func enrollmentRootChecks(p enrollmentPlan) string {
	s := ""
	for _, root := range p.Scope.Roots {
		r := shellQuote(root)
		s += "test \"$(readlink -m " + r + ")\" = " + r + "; if [ -e " + r + " ]; then test \"$(readlink -f " + r + ")\" = " + r + "; $SUDO runuser -u \"$user\" -- test -w " + r + "; else $SUDO install -d -m 0750 -o \"$user\" -g \"$group\" " + r + "; fi\n"
	}
	return s
}

func enrollmentSourceChecks(p enrollmentPlan) string {
	s := ""
	for _, v := range []struct{ file, hash string }{{"/usr/local/bin/logagent", p.Remote.BinarySHA}, {"/etc/logagent/agent.yaml", p.Remote.ConfigSHA}, {"/etc/systemd/system/logagent.service", p.Remote.UnitSHA}} {
		if v.hash == "" {
			s += "$SUDO test ! -e " + shellQuote(v.file) + "\n"
		} else {
			s += "test \"$($SUDO sha256sum " + shellQuote(v.file) + " | cut -d' ' -f1)\" = " + shellQuote(v.hash) + "\n"
		}
	}
	return s
}
