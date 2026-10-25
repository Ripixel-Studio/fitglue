#!/usr/bin/env bash
# Install the FitGlue systemd *user* units on the box (rethink F3.1). No sudo.
#
#   deploy/box/install.sh              symlink units, daemon-reload, enable fitglue.target
#   deploy/box/install.sh --no-enable  same, but leave the target disabled (nothing
#                                      starts at login/boot) - for the period when
#                                      Cloud Run is still serving and only ad-hoc
#                                      smoke tests run here
#
# Units are symlinked, not copied, so a `git pull` in the checkout changes them
# in place; run `systemctl --user daemon-reload` (make deploy-box does) after.
# systemd calls such units "linked": `systemctl --user disable <unit>` removes
# the symlink itself, not just the wants link. Re-run this script to restore it.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
dest=${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user
enable=1
case "${1:-}" in
  --no-enable) enable=0 ;;
  "") ;;
  *) echo "usage: $0 [--no-enable]" >&2; exit 2 ;;
esac

state=$(systemctl --user is-system-running 2>/dev/null || true)
case "$state" in
  running|degraded) ;;
  *) echo "systemd user manager not reachable (state: '${state:-none}'); is 'loginctl enable-linger $USER' set and XDG_RUNTIME_DIR exported?" >&2; exit 1 ;;
esac

mkdir -p "$dest"
for f in "$here"/fitglue-*.service "$here"/fitglue.target; do
  ln -sfn "$f" "$dest/$(basename "$f")"
  echo "linked $(basename "$f")"
done
systemctl --user daemon-reload

envfile=$HOME/.jarvis/fitglue.env
if [ ! -f "$envfile" ]; then
  echo "note: $envfile is missing; units will fail to start until F3.5 writes it (see fitglue.env.example)" >&2
fi
if [ ! -f "$HOME/.jarvis/fitglue-box-sa.json" ]; then
  echo "note: ~/.jarvis/fitglue-box-sa.json is missing; units will fail to reach GCP until F3.2 lands the key" >&2
fi

if [ "$enable" = 1 ]; then
  systemctl --user enable fitglue.target
  echo "fitglue.target enabled (not started). Start with: systemctl --user start fitglue.target"
else
  # Not `systemctl disable`: on a linked unit that deletes the unit symlink too.
  rm -f "$dest/default.target.wants/fitglue.target"
  systemctl --user daemon-reload
  echo "fitglue.target left disabled (--no-enable); state: $(systemctl --user is-enabled fitglue.target 2>&1)"
fi
systemctl --user list-units --all --no-pager --plain 'fitglue.target' 'fitglue-*.service'
