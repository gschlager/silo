# COPR calls this as: make -f .copr/Makefile srpm outdir=<dir> spec=<spec>
# (SCM source, build method "make srpm"). See .copr/srpm.sh.

COPR_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
outdir ?= $(COPR_DIR)../dist
spec ?= $(COPR_DIR)../silo.spec

.PHONY: srpm
srpm:
	sh $(COPR_DIR)srpm.sh "$(outdir)" "$(spec)"
