#!/usr/bin/env bash
# check-comments.sh — enforce English-only comments in Go source.
#
# Scans every *.go file (excluding vendored/build output) for CJK characters
# in comments (// line comments and /* */ block comments). CJK inside string
# literals is allowed — several tests legitimately use multi-byte rune
# fixtures. Markdown documentation may stay in any language.
set -euo pipefail
cd "$(dirname "$0")/.."

hits=$(find . -name '*.go' -not -path './console/*' -not -path './.git/*' -print0 |
  xargs -0 perl -Mopen=':std,:encoding(UTF-8)' -0775 -ne '
    # strip double-quoted and backquoted string literals (test fixtures may
    # legitimately contain CJK); then flag CJK in what remains only when it
    # appears inside a comment
    my $src = $_;
    $src =~ s/"(?:[^"\\]|\\.)*"//gs;
    $src =~ s/`[^`]*`//gs;
    while ($src =~ m{//[^\n]*}gs) {
      my $c = $&;
      print "$ARGV: $.: $c\n" if $c =~ /\p{Han}/;
    }
    while ($src =~ m{/\*.*?\*/}gs) {
      my $c = $&;
      print "$ARGV: block comment: $c\n" if $c =~ /\p{Han}/;
    }
  ' || true)

if [ -n "$hits" ]; then
  echo "CJK characters found in Go comments (comments must be English):"
  echo "$hits"
  exit 1
fi
echo "OK: all Go comments are CJK-free"
