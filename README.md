# fmtimports

For K8s related projects, please use https://github.com/kubernetes/kubeadm/blob/main/kinder/hack/verify-imports-order.sh

Tool for formatting golang import lines

```
stdlib

imports outside of *.k8s.io

*.k8s.io (non local repository)

local repository (k8s.io/kubernetes/.*)
```

# Install

```shell
go install github.com/xinydev/fmtimports@latest
```

# Usage

```shell
fmtimports --help

usage: gofmt-import [flags] [path ...]
  -d    display diffs instead of rewriting files
  -ignore-file string
        files with this string in the file name will be ignored (default "zz_generated")
  -l    list files whose formatting differs from gofmt's
  -w    write result to (source) file instead of stdout

```

# Example

```shell
./fmtimports testdata/1.input
```

Before:

```go
package main

import (
	bootstraptokenv1 "k8s.io/kubernetes/cmd/kubeadm/app/apis/bootstraptoken/v1"
	"k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
	"k8s.io/kubernetes/cmd/kubeadm/app/constants"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"

	fuzz "github.com/google/gofuzz"
	fuzz2 "github.com/google/gofuzz2"
	"os"
	"fmt"
)

```

After:

```go
package main

import (
	"fmt"
	"os"

	fuzz "github.com/google/gofuzz"
	fuzz2 "github.com/google/gofuzz2"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeserializer "k8s.io/apimachinery/pkg/runtime/serializer"

	bootstraptokenv1 "k8s.io/kubernetes/cmd/kubeadm/app/apis/bootstraptoken/v1"
	"k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
	"k8s.io/kubernetes/cmd/kubeadm/app/constants"
)

```

