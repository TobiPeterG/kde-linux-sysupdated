<!--
    SPDX-License-Identifier: CC0-1.0
    SPDX-FileCopyrightText: 2025 Harald Sitter <sitter@kde.org>
-->

Compares 2 **local** caibx files to figure out how many chunks are different.

e.g.

```bash
go build -o compare ./compare
compare/compare kde-linux_202508180839_root-x86-64.erofs.caibx kde-linux_202508181041_root-x86-64.erofs.caibx
```
