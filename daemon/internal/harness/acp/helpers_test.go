package acp_test

import "os"

func os_stat(p string) (os.FileInfo, error) { return os.Stat(p) }
