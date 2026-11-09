# The binary is built stripped (-s -w), so there is no debug info to package
%global debug_package %{nil}

Name:           silo
Version:        %{version}
Release:        1%{?dist}
Summary:        Secure isolated local environments for AI agents

License:        MIT
URL:            https://github.com/gschlager/silo
Source0:        https://github.com/gschlager/%{name}/archive/v%{version}.tar.gz

BuildRequires:  golang >= 1.27.3
BuildRequires:  systemd-rpm-macros
Requires:       incus
Requires:       nftables

%description
Silo creates and manages secure, isolated local development environments
for AI agents using Incus containers.

%prep
%setup -q -n %{name}-%{version}

%build
go build -ldflags="-s -w -X github.com/gschlager/silo/internal/cli.Version=%{version}" -o %{name} ./cmd/silo

%install
install -Dpm 0755 %{name} %{buildroot}%{_bindir}/%{name}
install -Dpm 0644 LICENSE %{buildroot}%{_licensedir}/%{name}/LICENSE
install -Dpm 0644 packaging/silo-incus-docker-fix.service %{buildroot}%{_unitdir}/silo-incus-docker-fix.service
install -Dpm 0644 packaging/80-silo.preset %{buildroot}%{_presetdir}/80-silo.preset

%post
%systemd_post silo-incus-docker-fix.service
# The incus units come from the incus package, whose %%post ran before our
# preset file existed, so apply the preset to them on first install.
if [ $1 -eq 1 ]; then
    systemctl preset incus.socket incus-user.socket incus-startup.service >/dev/null 2>&1 || :
fi

%preun
%systemd_preun silo-incus-docker-fix.service

%postun
%systemd_postun silo-incus-docker-fix.service

%files
%license LICENSE
%{_bindir}/%{name}
%{_unitdir}/silo-incus-docker-fix.service
%{_presetdir}/80-silo.preset
