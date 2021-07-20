#!/usr/bin/env bash


set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT=$(dirname "${BASH_SOURCE[0]}")/..

go build -o gofmt-import $REPO_ROOT/main.go

./$REPO_ROOT/gofmt-import $REPO_ROOT/hack/testdata/1.input > 1-test.output

if [ $(diff -u hack/testdata/1.golden 1-test.output| wc -l ) != 0 ]; then \
  		echo "Error: default" && exit 1; \
fi


./$REPO_ROOT/gofmt-import -r "^\"github.*\"$ ^\"k8s.*\"$" $REPO_ROOT/hack/testdata/1.input > 1-regex-test.output

if [ $(diff -u hack/testdata/1-regex.golden 1-regex-test.output| wc -l ) != 0 ]; then \
  		echo "Error: regex" && exit 1; \
fi
