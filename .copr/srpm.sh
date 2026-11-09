#!/bin/sh
# Builds the silo SRPM from the checked-out commit, for COPR or locally:
#
#   sh .copr/srpm.sh <outdir> [spec]
#
# - Version: the tag when the commit is tagged (v1.2.0 -> 1.2.0), otherwise a
#   snapshot after the last tag in Fedora's format, e.g. 0.0.0^20261109git1a2b3c4.
# - Go dependencies are vendored into the source tarball here, because this
#   step has network access. The RPM build itself then runs offline.
set -eu

outdir=$(mkdir -p "$1" && cd "$1" && pwd)
top=$(cd "$(dirname "$0")/.." && pwd)
spec=${2:-$top/silo.spec}
cd "$top"

# COPR runs this as root in a fresh chroot; locally the tools are usually there
if ! command -v go >/dev/null || ! command -v rpmbuild >/dev/null || ! command -v git >/dev/null; then
    dnf -y install git golang rpm-build
fi
# The clone can belong to another user than the one running this
git config --global --add safe.directory "$top"

commit=$(git rev-parse --short=7 HEAD)
if tag=$(git describe --tags --exact-match 2>/dev/null); then
    version=${tag#v}
else
    last=$(git describe --tags --abbrev=0 2>/dev/null || echo v0.0.0)
    version="${last#v}^$(git log -1 --format=%cd --date=format:%Y%m%d)git$commit"
fi
echo "Building silo $version"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
name="silo-$commit"

git archive --prefix="$name/" HEAD | tar -x -C "$work"
(cd "$work/$name" && GOTOOLCHAIN=auto go mod vendor)
tar -czf "$work/$name.tar.gz" -C "$work" "$name"

sed -e "s/^Version:.*/Version:        $version/" \
    -e "s|^Source0:.*|Source0:        $name.tar.gz|" \
    -e "s|^%setup .*|%setup -q -n $name|" \
    "$spec" >"$work/silo.spec"

rpmbuild -bs \
    --define "_sourcedir $work" \
    --define "_srcrpmdir $outdir" \
    "$work/silo.spec"
