# gofmt-import

Same as gofmt, but for import lines

# Install

```shell
go build -o gofmt-import github.com/engineerless/gofmt-import
```

# Usage

```shell
./gofmt-import -d testdata/*
```

## default mode

input:

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

after fotmat:

```go
package main

import (
	"bytes" // The first is the standard library
	"flag"
	"fmt"

	"github.com/bar" // The second is a third-party library
	"github.com/foo"
)
```

## Regex