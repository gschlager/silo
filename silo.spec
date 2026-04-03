Name:           silo
Version:        %{version}
Release:        1%{?dist}
Summary:        Secure isolated local environments for AI agents

License:        MIT
URL:            https://github.com/gschlager/silo
Source0:        https://github.com/gschlager/%{name}/archive/v%{version}.tar.gz

BuildRequires:  golang >= 1.26

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

%files
%license LICENSE
%{_bindir}/%{name}
