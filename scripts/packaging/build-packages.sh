#!/usr/bin/env bash
# build-packages.sh — Build OpenTorque DEB, RPM, or SUSE packages.
#
# Usage:
#   ./scripts/packaging/build-packages.sh [deb|rpm|suse] [version]
#
# Environment:
#   GOARCH  — Target architecture (amd64, arm64). Default: native.
#   GOOS    — Target OS. Default: linux.
#
# Outputs packages to dist/ directory.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

PKG_FORMAT="${1:-deb}"
VERSION="${2:-$(grep '^VERSION' "$PROJECT_ROOT/Makefile" | head -1 | awk -F= '{print $2}' | tr -d ' ')}"
VERSION="${VERSION:-0.1.0}"
RELEASE="1"
ARCH="${GOARCH:-$(go env GOARCH 2>/dev/null || echo amd64)}"

# Map Go arch to package arch names
case "$ARCH" in
  amd64) DEB_ARCH="amd64"; RPM_ARCH="x86_64" ;;
  arm64) DEB_ARCH="arm64"; RPM_ARCH="aarch64" ;;
  *)     DEB_ARCH="$ARCH";  RPM_ARCH="$ARCH" ;;
esac

DIST_DIR="$PROJECT_ROOT/dist"
BUILD_DIR="$DIST_DIR/build"
BIN_DIR="$PROJECT_ROOT/bin"

# Package file lists
SERVER_BINS="pbs_server pbs_sched"
CLIENT_BINS="pbs_mom momctl pbs_track pbs_pam_check"
CLI_BINS="qsub qstat qdel qhold qrls qrun qalter qrerun qmove qorder qsig qmsg \
          qstart qstop qenable qdisable qselect qchkpt qterm qmgr \
          pbsnodes pbsdsh tracejob printjob"

echo "=== OpenTorque Package Builder ==="
echo "  Format:  $PKG_FORMAT"
echo "  Version: $VERSION"
echo "  Arch:    $ARCH"
echo ""

# Step 1: Build all binaries
echo "[1/4] Building binaries..."
cd "$PROJECT_ROOT"
export PATH="$PATH:/usr/local/go/bin"
make clean >/dev/null 2>&1 || true
make all 2>&1 | tail -1

# Step 2: Prepare staging directories
echo "[2/4] Preparing package staging..."
rm -rf "$BUILD_DIR"

# Generate a shared auth_key at build time so all packages contain the same key
AUTH_KEY_HEX=$(openssl rand -hex 32)
echo "  Auth key generated (embedded in all packages)"

for pkg in server compute cli; do
  PKG_NAME="opentorque-${pkg}"
  STAGE="$BUILD_DIR/$PKG_NAME"

  mkdir -p "$STAGE/usr/local/sbin"
  mkdir -p "$STAGE/usr/local/bin"
  mkdir -p "$STAGE/etc/opentorque"
  mkdir -p "$STAGE/var/spool/torque/server_priv/jobs"
  mkdir -p "$STAGE/var/spool/torque/server_logs"
  mkdir -p "$STAGE/var/spool/torque/spool"

  # Embed the shared auth_key in every package
  echo "$AUTH_KEY_HEX" > "$STAGE/var/spool/torque/auth_key"
  chmod 644 "$STAGE/var/spool/torque/auth_key"

  case "$pkg" in
    server)
      # Daemons
      for b in $SERVER_BINS; do
        cp "$BIN_DIR/$b" "$STAGE/usr/local/sbin/"
      done
      # Systemd units
      mkdir -p "$STAGE/lib/systemd/system"
      cp "$PROJECT_ROOT/configs/systemd/pbs_server.service" "$STAGE/lib/systemd/system/"
      cp "$PROJECT_ROOT/configs/systemd/pbs_sched.service" "$STAGE/lib/systemd/system/"
      # Scheduler config
      cp "$PROJECT_ROOT/configs/sched_config.example" "$STAGE/etc/opentorque/sched_config.example"
      # Server-specific directories
      mkdir -p "$STAGE/var/spool/torque/server_priv/acl_svr"
      mkdir -p "$STAGE/var/spool/torque/sched_priv"
      mkdir -p "$STAGE/var/spool/torque/sched_logs"
      ;;
    compute)
      # MOM daemon + utilities
      for b in $CLIENT_BINS; do
        cp "$BIN_DIR/$b" "$STAGE/usr/local/sbin/"
      done
      # Systemd unit
      mkdir -p "$STAGE/lib/systemd/system"
      cp "$PROJECT_ROOT/configs/systemd/pbs_mom.service" "$STAGE/lib/systemd/system/"
      # MOM-specific directories
      mkdir -p "$STAGE/var/spool/torque/mom_priv/jobs"
      mkdir -p "$STAGE/var/spool/torque/mom_logs"
      mkdir -p "$STAGE/var/spool/torque/aux"
      mkdir -p "$STAGE/var/spool/torque/undelivered"
      ;;
    cli)
      # CLI tools
      for b in $CLI_BINS; do
        cp "$BIN_DIR/$b" "$STAGE/usr/local/bin/"
      done
      # PATH setup for systems where /usr/local/bin is not in default PATH
      mkdir -p "$STAGE/etc/profile.d"
      cat > "$STAGE/etc/profile.d/opentorque.sh" <<'PROF'
# OpenTorque CLI tools PATH
case ":$PATH:" in
  *:/usr/local/bin:*) ;;
  *) export PATH="/usr/local/bin:$PATH" ;;
esac
PROF
      chmod 644 "$STAGE/etc/profile.d/opentorque.sh"
      ;;
  esac
done

# Step 3: Build packages
echo "[3/4] Building $PKG_FORMAT packages..."
mkdir -p "$DIST_DIR"

build_deb() {
  local pkg="$1" desc="$2" deps="$3"
  local PKG_NAME="opentorque-${pkg}"
  local STAGE="$BUILD_DIR/$PKG_NAME"

  mkdir -p "$STAGE/DEBIAN"

  # Control file
  cat > "$STAGE/DEBIAN/control" <<EOF
Package: $PKG_NAME
Version: ${VERSION}-${RELEASE}
Section: admin
Priority: optional
Architecture: $DEB_ARCH
Depends: $deps
Replaces: opentorque-server, opentorque-compute, opentorque-cli
Maintainer: OpenTorque Project <opentorque@example.com>
Homepage: https://github.com/xinlaoda/opentorque
Description: $desc
 OpenTorque is a modern Go reimplementation of the TORQUE/PBS
 resource manager for HPC cluster job scheduling.
EOF

  # Post-install script
  cat > "$STAGE/DEBIAN/postinst" <<'POSTINST'
#!/bin/bash
set -e

PBS_HOME="/var/spool/torque"

# Ensure directory permissions
chmod 755 "$PBS_HOME" 2>/dev/null || true
POSTINST

  # Package-specific postinst additions
  case "$pkg" in
    server)
      cat >> "$STAGE/DEBIAN/postinst" <<'POSTINST'

# Auth key is pre-generated and included in the package.
# Ensure correct permissions.
AUTH_KEY="$PBS_HOME/auth_key"
chmod 644 "$AUTH_KEY" 2>/dev/null || true

# Set server_name
HOSTNAME=$(hostname -s)
echo "$HOSTNAME" > "$PBS_HOME/server_name"

# Create nodes file if missing (must be a regular file, not a directory)
if [ ! -f "$PBS_HOME/server_priv/nodes" ]; then
  rm -rf "$PBS_HOME/server_priv/nodes" 2>/dev/null || true
  touch "$PBS_HOME/server_priv/nodes"
  chmod 644 "$PBS_HOME/server_priv/nodes"
fi

# Install scheduler config if missing
SCHED_CONF="$PBS_HOME/sched_priv/sched_config"
if [ ! -f "$SCHED_CONF" ]; then
  mkdir -p "$PBS_HOME/sched_priv"
  cp /etc/opentorque/sched_config.example "$SCHED_CONF"
fi

# Reload systemd
systemctl daemon-reload 2>/dev/null || true
echo ""
echo "=== OpenTorque Server installed ==="
echo "  Initialize:  sudo pbs_server -t create"
echo "  Start:       sudo systemctl start pbs_server pbs_sched"
echo "  Auth key:    $AUTH_KEY (pre-installed, same key in all packages)"
POSTINST
      ;;
    compute)
      cat >> "$STAGE/DEBIAN/postinst" <<'POSTINST'

# Create MOM config if missing
MOM_CONF="$PBS_HOME/mom_priv/config"
if [ ! -f "$MOM_CONF" ]; then
  mkdir -p "$PBS_HOME/mom_priv"
  HOSTNAME=$(hostname -s)
  echo "\$pbsserver $HOSTNAME" > "$MOM_CONF"
  echo "NOTE: Edit $MOM_CONF to set the correct server hostname."
fi

# Auth key is pre-installed from the package
chmod 644 "$PBS_HOME/auth_key" 2>/dev/null || true

# Reload systemd
systemctl daemon-reload 2>/dev/null || true
echo ""
echo "=== OpenTorque Compute (MOM) installed ==="
echo "  Configure: Edit $PBS_HOME/mom_priv/config"
echo "  Auth key:  Pre-installed at $PBS_HOME/auth_key"
echo "  Start:     sudo systemctl start pbs_mom"
POSTINST
      ;;
    cli)
      cat >> "$STAGE/DEBIAN/postinst" <<'POSTINST'

# Set server_name if missing
if [ ! -f "$PBS_HOME/server_name" ]; then
  echo "localhost" > "$PBS_HOME/server_name"
  echo "NOTE: Edit $PBS_HOME/server_name to point to your PBS server."
fi

# Auth key is pre-installed from the package
chmod 644 "$PBS_HOME/auth_key" 2>/dev/null || true

echo ""
echo "=== OpenTorque CLI tools installed ==="
echo "  Configure: Set server in $PBS_HOME/server_name"
echo "  Auth key:  Pre-installed at $PBS_HOME/auth_key"
POSTINST
      ;;
  esac

  chmod 755 "$STAGE/DEBIAN/postinst"

  # Pre-removal script
  cat > "$STAGE/DEBIAN/prerm" <<'PRERM'
#!/bin/bash
set -e
PRERM

  case "$pkg" in
    server)
      cat >> "$STAGE/DEBIAN/prerm" <<'PRERM'
systemctl stop pbs_server pbs_sched 2>/dev/null || true
systemctl disable pbs_server pbs_sched 2>/dev/null || true
PRERM
      ;;
    compute)
      cat >> "$STAGE/DEBIAN/prerm" <<'PRERM'
systemctl stop pbs_mom 2>/dev/null || true
systemctl disable pbs_mom 2>/dev/null || true
PRERM
      ;;
  esac
  chmod 755 "$STAGE/DEBIAN/prerm"

  # Conffiles
  if [ "$pkg" = "server" ]; then
    echo "/etc/opentorque/sched_config.example" > "$STAGE/DEBIAN/conffiles"
  fi

  # Build the .deb
  dpkg-deb --build --root-owner-group "$STAGE" "$DIST_DIR/${PKG_NAME}_${VERSION}-${RELEASE}_${DEB_ARCH}.deb"
}

build_rpm() {
  local pkg="$1" desc="$2" deps="$3"
  local PKG_NAME="opentorque-${pkg}"
  local STAGE="$BUILD_DIR/$PKG_NAME"

  # Build RPM topdir structure
  local RPM_TOP="$BUILD_DIR/rpmbuild-${pkg}"
  mkdir -p "$RPM_TOP"/{BUILD,RPMS,SOURCES,SPECS,SRPMS}

  # Create tarball from staging
  local TAR_NAME="${PKG_NAME}-${VERSION}"
  mkdir -p "$RPM_TOP/SOURCES"
  (cd "$BUILD_DIR" && cp -a "$PKG_NAME" "$TAR_NAME" && tar czf "$RPM_TOP/SOURCES/${TAR_NAME}.tar.gz" "$TAR_NAME" && rm -rf "$TAR_NAME")

  # RPM spec
  cat > "$RPM_TOP/SPECS/${PKG_NAME}.spec" <<EOF
Name:           $PKG_NAME
Version:        $VERSION
Release:        ${RELEASE}%{?dist}
Summary:        $desc
License:        Apache-2.0
URL:            https://github.com/xinlaoda/opentorque
Source0:        %{name}-%{version}.tar.gz
BuildArch:      $RPM_ARCH

%description
OpenTorque is a modern Go reimplementation of the TORQUE/PBS
resource manager for HPC cluster job scheduling.

%prep
%setup -q

%install
cp -a * %{buildroot}/

%files
EOF

  # Add file list to spec based on package
  case "$pkg" in
    server)
      cat >> "$RPM_TOP/SPECS/${PKG_NAME}.spec" <<'EOF'
/usr/local/sbin/pbs_server
/usr/local/sbin/pbs_sched
/lib/systemd/system/pbs_server.service
/lib/systemd/system/pbs_sched.service
/etc/opentorque/sched_config.example
%config(noreplace) /var/spool/torque/auth_key
%dir /var/spool/torque
%dir /var/spool/torque/server_priv
%dir /var/spool/torque/server_priv/jobs
%dir /var/spool/torque/server_priv/acl_svr
%dir /var/spool/torque/server_logs
%dir /var/spool/torque/sched_priv
%dir /var/spool/torque/sched_logs
%dir /var/spool/torque/spool

%post
PBS_HOME="/var/spool/torque"
chmod 644 "$PBS_HOME/auth_key" 2>/dev/null || true
echo "$(hostname -s)" > "$PBS_HOME/server_name"
if [ ! -f "$PBS_HOME/server_priv/nodes" ]; then
  rm -rf "$PBS_HOME/server_priv/nodes" 2>/dev/null || true
  touch "$PBS_HOME/server_priv/nodes"
  chmod 644 "$PBS_HOME/server_priv/nodes"
fi
if [ ! -f "$PBS_HOME/sched_priv/sched_config" ]; then
  cp /etc/opentorque/sched_config.example "$PBS_HOME/sched_priv/sched_config"
fi
systemctl daemon-reload 2>/dev/null || true

%preun
systemctl stop pbs_server pbs_sched 2>/dev/null || true
EOF
      ;;
    compute)
      cat >> "$RPM_TOP/SPECS/${PKG_NAME}.spec" <<'EOF'
/usr/local/sbin/pbs_mom
/usr/local/sbin/momctl
/usr/local/sbin/pbs_track
/usr/local/sbin/pbs_pam_check
/lib/systemd/system/pbs_mom.service
%config(noreplace) /var/spool/torque/auth_key
%dir /var/spool/torque
%dir /var/spool/torque/mom_priv
%dir /var/spool/torque/mom_priv/jobs
%dir /var/spool/torque/mom_logs
%dir /var/spool/torque/aux
%dir /var/spool/torque/undelivered
%dir /var/spool/torque/spool

%post
PBS_HOME="/var/spool/torque"
MOM_CONF="$PBS_HOME/mom_priv/config"
if [ ! -f "$MOM_CONF" ]; then
  echo "\$pbsserver $(hostname -s)" > "$MOM_CONF"
fi
systemctl daemon-reload 2>/dev/null || true

%preun
systemctl stop pbs_mom 2>/dev/null || true
EOF
      ;;
    cli)
      cat >> "$RPM_TOP/SPECS/${PKG_NAME}.spec" <<'EOF'
EOF
      for b in $CLI_BINS; do
        echo "/usr/local/bin/$b" >> "$RPM_TOP/SPECS/${PKG_NAME}.spec"
      done
      cat >> "$RPM_TOP/SPECS/${PKG_NAME}.spec" <<'EOF'
/etc/profile.d/opentorque.sh
%config(noreplace) /var/spool/torque/auth_key
%dir /var/spool/torque

%post
PBS_HOME="/var/spool/torque"
if [ ! -f "$PBS_HOME/server_name" ]; then
  echo "localhost" > "$PBS_HOME/server_name"
fi
EOF
      ;;
  esac

  rpmbuild --define "_topdir $RPM_TOP" -bb "$RPM_TOP/SPECS/${PKG_NAME}.spec" 2>&1 | tail -3
  find "$RPM_TOP/RPMS" -name "*.rpm" -exec cp {} "$DIST_DIR/" \;
}

case "$PKG_FORMAT" in
  deb)
    build_deb "server" "OpenTorque server and scheduler daemons" ""
    build_deb "compute" "OpenTorque compute node (MOM) daemon" ""
    build_deb "cli"    "OpenTorque CLI tools for job management" ""
    ;;
  rpm)
    build_rpm "server" "OpenTorque server and scheduler daemons" ""
    build_rpm "compute" "OpenTorque compute node (MOM) daemon" ""
    build_rpm "cli"    "OpenTorque CLI tools for job management" ""
    ;;
  suse)
    # SUSE uses RPM but with zypper; same spec works
    build_rpm "server" "OpenTorque server and scheduler daemons" ""
    build_rpm "compute" "OpenTorque compute node (MOM) daemon" ""
    build_rpm "cli"    "OpenTorque CLI tools for job management" ""
    ;;
  all)
    echo "--- Building DEB packages ---"
    build_deb "server" "OpenTorque server and scheduler daemons" ""
    build_deb "compute" "OpenTorque compute node (MOM) daemon" ""
    build_deb "cli"    "OpenTorque CLI tools for job management" ""
    echo "--- Building RPM packages ---"
    build_rpm "server" "OpenTorque server and scheduler daemons" ""
    build_rpm "compute" "OpenTorque compute node (MOM) daemon" ""
    build_rpm "cli"    "OpenTorque CLI tools for job management" ""
    ;;
  *)
    echo "Usage: $0 [deb|rpm|suse|all] [version]"
    exit 1
    ;;
esac

echo ""
echo "[4/4] Packages built successfully!"
echo ""
ls -lh "$DIST_DIR"/*.{deb,rpm} 2>/dev/null
echo ""
echo "Done."
