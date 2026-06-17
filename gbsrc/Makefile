#
# MediaBoy GB/GBC build system
#
# Usage:
#   make PROJECTNAME=myimage          — build ROM from exported files
#   make clean                        — remove obj/
#
# The source files (main.c, {PROJECTNAME}.c, {PROJECTNAME}.h) are generated
# by the MediaBoy Go tool. Run "Export GBDK" then "Compile GB ROM" from the UI,
# or run the generated compile.bat directly.
#

ifndef GBDK_HOME
	GBDK_HOME = C:\Bin\gbdk
endif

LCC        = $(GBDK_HOME)/bin/lcc
LCCFLAGS  += -Wm-yC -Wl-yt0x19 -Wl-yo4

ifdef GBDK_DEBUG
	LCCFLAGS += -debug -v
endif

# Set by MediaBoy export / pass on command line
ifndef PROJECTNAME
	PROJECTNAME = image
endif

OBJDIR  = obj
BINS    = $(OBJDIR)/$(PROJECTNAME).gb

# Source files expected in the same directory as Makefile (the output dir)
CSOURCES = main.c $(PROJECTNAME).c

OBJS = $(patsubst %.c,$(OBJDIR)/%.o,$(CSOURCES))

all: prepare $(BINS)

prepare:
	mkdir -p $(OBJDIR)

$(OBJDIR)/%.o: %.c
	$(LCC) $(LCCFLAGS) -c -o $@ $<

$(BINS): $(OBJS)
	$(LCC) $(LCCFLAGS) -o $@ $(OBJS)
	@echo "ROM ready: $@"

clean:
	rm -f $(OBJDIR)/*.o $(OBJDIR)/*.gb $(OBJDIR)/*.ihx
