# Makefile integration example.
#
# Each target reports progress with `barmux emit` (which no-ops when no
# dashboard is listening, so `make -j` works with or without barmux):
#
#   barmux run -- make -j3 -f examples/Makefile all
#
# `emit` never blocks and never fails the build: if the parent dashboard
# is gone, lines are silently dropped and make carries on.

SRCS := one two three four

.PHONY: all compile docs package

all: compile docs package
	@barmux emit log make: all targets complete

compile:
	@barmux emit start compile 4 Compiling sources
	@for s in $(SRCS); do \
		sleep 0.1; \
		barmux emit tick compile; \
		barmux emit msg compile cc $$s.c; \
	done
	@barmux emit done compile

docs:
	@barmux emit start docs 2 Rendering docs
	@sleep 0.2; barmux emit tick docs
	@sleep 0.2; barmux emit tick docs
	@barmux emit done docs

package:
	@barmux emit start package - Packaging artifacts
	@sleep 0.3; barmux emit tick package
	@sleep 0.3; barmux emit tick package
	@barmux emit done package
