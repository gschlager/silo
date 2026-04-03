.PHONY: srpm

srpm:
	dnf -y install golang rpm-build
	spectool -g -R ../silo.spec
	rpmbuild -bs --define "_sourcedir $$(rpm --eval '%{_topdir}')/SOURCES" --define "version $$(git describe --tags --abbrev=0 | sed 's/^v//')" ../silo.spec --resultdir $(outdir)
