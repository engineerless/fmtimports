# gofmt-import

Tool for formatting golang import lines

# Install

```shell
go install github.com/engineerless/gofmt-import@latest
```

# Usage

## Default mode

```shell
./gofmt-import testdata/1.input
```

Before:

```go
package main

import (
	"flag"
	"fmt"
	"fmt"
	"github.com/bar"
	"github.com/foo"
)
```

After:

```go
package main

import (
	"bytes" // The first is standard libraries
	"flag"
	"fmt"

	"github.com/bar" // The second is third-party libraries
	"github.com/foo"
)
```

## Regex

```shell
./gofmt-import -r "^\"github.*\"$ ^\"k8s.*\"$"   testdata/1.input
```

Before:

```go
package main

import (
	"fmt"
	"github.com/bar"
	"github.com/foo"
	k8sbar "k8s.io/bar"
	k8sfoo "k8s.io/foo"
)

```

After:

```go
package main

import (
	"fmt" // The first is standard libraries

	"github.com/bar" // imports which match ^\"github.*\"$
	"github.com/foo"

	k8sbar "k8s.io/bar" // imports which match ^\"k8s.*\"$
	k8sfoo "k8s.io/foo"
)

```

