# The binary is built stripped (-s -w), so there is no debug info to package
%global debug_package %{nil}

Name:           silo
# Version, Source0 and the %setup directory are filled in by .copr/srpm.sh
Version:        0
Release:        1%{?dist}
Summary:        Secure isolated local environments for AI agents

License:        MIT
URL:            https://github.com/gschlager/silo
Source0:        %{name}.tar.gz

BuildRequires:  golang >= 1.27.3
BuildRequires:  systemd-rpm-macros
Requires:       incus
Requires:       nftables
# certutil, to trust silo's local CA in Chrome's and Firefox's NSS databases
Recommends:     nss-tools

%description
Silo creates and manages secure, isolated local development environments
for AI agents using Incus containers.

%prep
%setup -q -n %{name}-%{version}

%build
go build -mod=vendor -ldflags="-s -w -X github.com/gschlager/silo/internal/cli.Version=%{version}" -o %{name} ./cmd/silo

%install
install -Dpm 0755 %{name} %{buildroot}%{_bindir}/%{name}
install -Dpm 0644 LICENSE %{buildroot}%{_licensedir}/%{name}/LICENSE
install -Dpm 0644 packaging/silo-incus-docker-fix.service %{buildroot}%{_unitdir}/silo-incus-docker-fix.service
install -Dpm 0644 packaging/80-silo.preset %{buildroot}%{_presetdir}/80-silo.preset
%{_userunitdir}/silo.socket
%{_userunitdir}/silo.service
%{_userpresetdir}/80-silo.preset
install -Dpm 0644 packaging/silo.socket %{buildroot}%{_userunitdir}/silo.socket
install -Dpm 0644 packaging/silo.service %{buildroot}%{_userunitdir}/silo.service
install -Dpm 0644 packaging/80-silo-user.preset %{buildroot}%{_userpresetdir}/80-silo.preset

%post
%systemd_post silo-incus-docker-fix.service
# Enables the silo service for every user from their next login on (through
# the user preset). silo setup starts it right away.
%systemd_user_post silo.socket silo.service
# The incus units come from the incus package, whose %%post ran before our
# preset file existed, so apply the preset to them on first install.
if [ $1 -eq 1 ]; then
    systemctl preset incus.socket incus-user.socket incus-startup.service >/dev/null 2>&1 || :
fi

%preun
%systemd_preun silo-incus-docker-fix.service
%systemd_user_preun silo.socket silo.service

%postun
%systemd_postun silo-incus-docker-fix.service

%files
%license LICENSE
# Lets the silo service's .localhost proxy listen on ports 80 and 443. Only
# this capability; silo can bind low ports and nothing else.
%caps(cap_net_bind_service=ep) %{_bindir}/%{name}
%{_unitdir}/silo-incus-docker-fix.service
%{_presetdir}/80-silo.preset
%{_userunitdir}/silo.socket
%{_userunitdir}/silo.service
%{_userpresetdir}/80-silo.preset
