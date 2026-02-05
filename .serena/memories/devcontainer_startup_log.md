# devcontainer startup log

```shell
What's next:
    Try Docker Debug for seamless, persistent debugging tools in any container or image → docker debug 0d829231969e3e298405b95fd492c30a6d54329ba5554cca74b6fd13e99e9097
    Learn more at https://docs.docker.com/go/debug-cli/
[58616 ms] Initializing configuration support...
[58616 ms] Internal initialization of dev container support package...
[58624 ms] Port forwarding connection from 51926 > 37513 > 37513 in the container.
[58624 ms] Start: Run in container: /home/node/.vscode-server/bin/c9d77990917f3102ada88be140d28b038d1dd7c7/node -e
[58713 ms] Port forwarding 51926 > 37513 > 37513 stderr: Connection established
[58728 ms] Port forwarding connection from 51927 > 37513 > 37513 in the container.
[58728 ms] Start: Run in container: /home/node/.vscode-server/bin/c9d77990917f3102ada88be140d28b038d1dd7c7/node -e
[58731 ms] [11:13:46] [127.0.0.1][2ba40ff4][ManagementConnection] New connection established.
[58749 ms] [11:13:46] Log level changed to info
[58791 ms] Port forwarding 51927 > 37513 > 37513 stderr: Connection established
[58835 ms] [11:13:46] [127.0.0.1][29e3eae6][ExtensionHostConnection] New connection established.
[58839 ms] [11:13:46] [127.0.0.1][29e3eae6][ExtensionHostConnection] <13629> Launched Extension Host Process.
[59032 ms] [11:13:46] [127.0.0.1][29e3eae6][ExtensionHostConnection] <13629> [reconnection-grace-time] Extension host: read VSCODE_RECONNECTION_GRACE_TIME=10800000ms (10800s)

[59035 ms] [11:13:46] #7: https://anthropic.gallerycdn.vsassets.io/extensions/anthropic/claude-code/2.1.32/1770313476154/Microsoft.VisualStudio.Code.Manifest?targetPlatform=linux-arm64 - error GET AggregateError [EHOSTUNREACH]:
[59037 ms] [11:13:46] #8: https://dbaeumer.gallerycdn.vsassets.io/extensions/dbaeumer/vscode-eslint/3.0.20/1765182448101/Microsoft.VisualStudio.Code.Manifest - error GET AggregateError [EHOSTUNREACH]:
[59074 ms] [11:13:47] #9: https://esbenp.gallerycdn.vsassets.io/extensions/esbenp/prettier-vscode/12.3.0/1769038951615/Microsoft.VisualStudio.Code.Manifest - error GET AggregateError [EHOSTUNREACH]:
[59083 ms] [11:13:47] #10: https://eamodio.gallerycdn.vsassets.io/extensions/eamodio/gitlens/17.9.0/1768340732093/Microsoft.VisualStudio.Code.Manifest - error GET AggregateError [EHOSTUNREACH]:
[59384 ms] [11:13:47] #11: https://anthropic.gallery.vsassets.io/_apis/public/gallery/publisher/anthropic/extension/claude-code/2.1.32/assetbyname/Microsoft.VisualStudio.Code.Manifest?targetPlatform=linux-arm64 - error GET AggregateError [EHOSTUNREACH]:
[11:13:47] AggregateError [EHOSTUNREACH]:
    at internalConnectMultiple (node:net:1134:18)
    at afterConnectMultiple (node:net:1715:7)
[59385 ms] [11:13:47] #12: https://dbaeumer.gallery.vsassets.io/_apis/public/gallery/publisher/dbaeumer/extension/vscode-eslint/3.0.20/assetbyname/Microsoft.VisualStudio.Code.Manifest - error GET AggregateError [EHOSTUNREACH]:
[11:13:47] AggregateError [EHOSTUNREACH]:
    at internalConnectMultiple (node:net:1134:18)
    at afterConnectMultiple (node:net:1715:7)
[59432 ms] [11:13:47] #14: https://eamodio.gallery.vsassets.io/_apis/public/gallery/publisher/eamodio/extension/gitlens/17.9.0/assetbyname/Microsoft.VisualStudio.Code.Manifest - error GET AggregateError [EHOSTUNREACH]:
[11:13:47] AggregateError [EHOSTUNREACH]:
    at internalConnectMultiple (node:net:1134:18)
    at afterConnectMultiple (node:net:1715:7)
[59432 ms] [11:13:47] #13: https://esbenp.gallery.vsassets.io/_apis/public/gallery/publisher/esbenp/extension/prettier-vscode/12.3.0/assetbyname/Microsoft.VisualStudio.Code.Manifest - error GET AggregateError [EHOSTUNREACH]:
[11:13:47] AggregateError [EHOSTUNREACH]:
    at internalConnectMultiple (node:net:1134:18)
    at afterConnectMultiple (node:net:1715:7)
[59433 ms] [11:13:47] Error: Failed Installing Extensions: anthropic.claude-code, dbaeumer.vscode-eslint, eamodio.gitlens, esbenp.prettier-vscode
    at Ml.installExtensions (file:///vscode/vscode-server/bin/linux-arm64/c9d77990917f3102ada88be140d28b038d1dd7c7/out/server-main.js:67:36583)
    at process.processTicksAndRejections (node:internal/process/task_queues:105:5)
[59434 ms] [11:13:47] Relaying the following extensions to install later: anthropic.claude-code, dbaeumer.vscode-eslint, esbenp.prettier-vscode, eamodio.gitlens
[60986 ms] [11:13:48] Downloaded extension to file:///home/node/.vscode-server/extensionsCache/972943fc-0196-4d6e-a940-8fba8bb0d340
[60995 ms] [11:13:48] Installing extension: dbaeumer.vscode-eslint {
  isMachineScoped: false,
  installPreReleaseVersion: false,
  isApplicationScoped: true,
  downloadExtensionsLocally: true,
  donotVerifySignature: false,
  donotIncludePackAndDependencies: true,
  keepExisting: true,
  profileLocation: qr {
    scheme: 'file',
    authority: '',
    path: '/home/node/.vscode-server/extensions/extensions.json',
    query: '',
    fragment: '',
    _formatted: 'file:///home/node/.vscode-server/extensions/extensions.json',
    _fsPath: '/home/node/.vscode-server/extensions/extensions.json'
  },
  productVersion: { version: '1.108.2', date: '2026-01-21T13:52:09.270Z' }
}
[11:13:48] Installing the extension without checking dependencies and pack dbaeumer.vscode-eslint
[61026 ms] [11:13:48] Extracted extension to file:///home/node/.vscode-server/extensions/dbaeumer.vscode-eslint-3.0.20: dbaeumer.vscode-eslint
[61030 ms] [11:13:48] Renamed to /home/node/.vscode-server/extensions/dbaeumer.vscode-eslint-3.0.20
[61037 ms] [11:13:48] Extension installed successfully: dbaeumer.vscode-eslint file:///home/node/.vscode-server/extensions/extensions.json
[61244 ms] Start: Run in container: mkdir -p '/vscode/vscode-server/extensionsCache' && cd '/home/node/.vscode-server/extensionsCache' && cp '5b6ba81e-188e-485c-9f23-2b373ac29608' 'b6c3ff66-2893-487c-889b-4d63f787f11b' '/vscode/vscode-server/extensionsCache'
[61251 ms]
[61251 ms]
[61251 ms] Start: Run in container: cd '/vscode/vscode-server/extensionsCache' && ls -t | tail -n +500 | xargs rm -f
[61254 ms]
[61254 ms]
[61999 ms] [11:13:49] Downloaded extension to file:///home/node/.vscode-server/extensionsCache/2fee1d8e-14c3-44fe-9390-42a4c8a8a7fe
[62011 ms] [11:13:49] Installing extension: eamodio.gitlens {
  isMachineScoped: false,
  installPreReleaseVersion: false,
  isApplicationScoped: true,
  downloadExtensionsLocally: true,
  donotVerifySignature: false,
  donotIncludePackAndDependencies: true,
  keepExisting: true,
  profileLocation: qr {
    scheme: 'file',
    authority: '',
    path: '/home/node/.vscode-server/extensions/extensions.json',
    query: '',
    fragment: '',
    _formatted: 'file:///home/node/.vscode-server/extensions/extensions.json',
    _fsPath: '/home/node/.vscode-server/extensions/extensions.json'
  },
  productVersion: { version: '1.108.2', date: '2026-01-21T13:52:09.270Z' }
}
[11:13:49] Installing the extension without checking dependencies and pack eamodio.gitlens
[62195 ms] [11:13:50] Downloaded extension to file:///home/node/.vscode-server/extensionsCache/b6c3ff66-2893-487c-889b-4d63f787f11b
[62206 ms] [11:13:50] Installing extension: github.copilot-chat {
  isApplicationScoped: false,
  profileLocation: qr {
    scheme: 'file',
    authority: '',
    path: '/home/node/.vscode-server/extensions/extensions.json',
    query: '',
    fragment: '',
    _formatted: 'file:///home/node/.vscode-server/extensions/extensions.json',
    _fsPath: '/home/node/.vscode-server/extensions/extensions.json'
  },
  productVersion: { version: '1.108.2', date: '2026-01-21T13:52:09.270Z' }
}
[62286 ms] [11:13:50] Getting Manifest... github.copilot
[62358 ms] [11:13:50] #18: https://GitHub.gallerycdn.vsassets.io/extensions/github/copilot/1.388.0/1761326434179/Microsoft.VisualStudio.Code.Manifest - error GET AggregateError [EHOSTUNREACH]:
[62366 ms] [11:13:50] Extracted extension to file:///home/node/.vscode-server/extensions/eamodio.gitlens-17.9.0: eamodio.gitlens
[62380 ms] [11:13:50] Renamed to /home/node/.vscode-server/extensions/eamodio.gitlens-17.9.0
[62389 ms] [11:13:50] Extension installed successfully: eamodio.gitlens file:///home/node/.vscode-server/extensions/extensions.json
[62454 ms] Port forwarding connection from 51957 > 37513 > 37513 in the container.
[62455 ms] Start: Run in container: /home/node/.vscode-server/bin/c9d77990917f3102ada88be140d28b038d1dd7c7/node -e
[62483 ms] Port forwarding connection from 51958 > 37513 > 37513 in the container.
[62483 ms] Start: Run in container: /home/node/.vscode-server/bin/c9d77990917f3102ada88be140d28b038d1dd7c7/node -e
[62557 ms] Port forwarding 51957 > 37513 > 37513 stderr: Connection established
[62593 ms] Port forwarding 51958 > 37513 > 37513 stderr: Connection established
[62706 ms] [11:13:50] #19: https://GitHub.gallery.vsassets.io/_apis/public/gallery/publisher/GitHub/extension/copilot/1.388.0/assetbyname/Microsoft.VisualStudio.Code.Manifest - error GET AggregateError [EHOSTUNREACH]:
[62706 ms] [11:13:50] Skipping the packed extension as it cannot be installed github.copilot AggregateError [EHOSTUNREACH]:
[62739 ms] [11:13:50] Downloaded extension to file:///home/node/.vscode-server/extensionsCache/5b6ba81e-188e-485c-9f23-2b373ac29608
[62748 ms] [11:13:50] Installing extension: github.copilot {
  isApplicationScoped: false,
  profileLocation: qr {
    scheme: 'file',
    authority: '',
    path: '/home/node/.vscode-server/extensions/extensions.json',
    query: '',
    fragment: '',
    _formatted: 'file:///home/node/.vscode-server/extensions/extensions.json',
    _fsPath: '/home/node/.vscode-server/extensions/extensions.json'
  },
  productVersion: { version: '1.108.2', date: '2026-01-21T13:52:09.270Z' }
}
[63127 ms] [11:13:51] Extracted extension to file:///home/node/.vscode-server/extensions/github.copilot-chat-0.36.2: github.copilot-chat
[63135 ms] [11:13:51] Renamed to /home/node/.vscode-server/extensions/github.copilot-chat-0.36.2
[63156 ms] [11:13:51] Extension installed successfully: github.copilot-chat file:///home/node/.vscode-server/extensions/extensions.json
[63168 ms] [11:13:51] Getting Manifest... github.copilot-chat
[63176 ms] [11:13:51] #26: https://GitHub.gallerycdn.vsassets.io/extensions/github/copilot-chat/0.36.2/1769117116935/Microsoft.VisualStudio.Code.Manifest - error GET AggregateError [EHOSTUNREACH]:
[63360 ms] [11:13:51] Skipping the packed extension as it cannot be installed github.copilot-chat AggregateError [EHOSTUNREACH]:
[63361 ms] [11:13:51] #27: https://GitHub.gallery.vsassets.io/_apis/public/gallery/publisher/GitHub/extension/copilot-chat/0.36.2/assetbyname/Microsoft.VisualStudio.Code.Manifest - error GET AggregateError [EHOSTUNREACH]:
[63865 ms] [11:13:51] Extracted extension to file:///home/node/.vscode-server/extensions/github.copilot-1.388.0: github.copilot
[63870 ms] [11:13:51] Renamed to /home/node/.vscode-server/extensions/github.copilot-1.388.0
[63886 ms] [11:13:51] Extension installed successfully: github.copilot file:///home/node/.vscode-server/extensions/extensions.json
[64716 ms] [11:13:52] Downloaded extension to file:///home/node/.vscode-server/extensionsCache/a292e8f9-b969-4fac-a4ea-1398a595312c
[64719 ms] [11:13:52] Installing extension: esbenp.prettier-vscode {
  isMachineScoped: false,
  installPreReleaseVersion: false,
  isApplicationScoped: true,
  downloadExtensionsLocally: true,
  donotVerifySignature: false,
  donotIncludePackAndDependencies: true,
  keepExisting: true,
  profileLocation: qr {
    scheme: 'file',
    authority: '',
    path: '/home/node/.vscode-server/extensions/extensions.json',
    query: '',
    fragment: '',
    _formatted: 'file:///home/node/.vscode-server/extensions/extensions.json',
    _fsPath: '/home/node/.vscode-server/extensions/extensions.json'
  },
  productVersion: { version: '1.108.2', date: '2026-01-21T13:52:09.270Z' }
}
[11:13:52] Installing the extension without checking dependencies and pack esbenp.prettier-vscode
[64931 ms] [11:13:52] Extracted extension to file:///home/node/.vscode-server/extensions/esbenp.prettier-vscode-12.3.0: esbenp.prettier-vscode
[64936 ms] [11:13:52] Renamed to /home/node/.vscode-server/extensions/esbenp.prettier-vscode-12.3.0
[64947 ms] [11:13:52] Extension installed successfully: esbenp.prettier-vscode file:///home/node/.vscode-server/extensions/extensions.json
[68575 ms] Port forwarding 51957 > 37513 > 37513 stderr: Remote close
[68596 ms] Port forwarding 51957 > 37513 > 37513 terminated with code 0 and signal null.
[69219 ms] Port forwarding 51958 > 37513 > 37513 stderr: Remote close
[69230 ms] Port forwarding 51958 > 37513 > 37513 terminated with code 0 and signal null.
[75374 ms] [11:14:03] Downloaded extension to file:///home/node/.vscode-server/extensionsCache/7a3c8671-fe5d-46cf-be6d-f34a358ee271
[75377 ms] [11:14:03] Installing extension: anthropic.claude-code {
  isMachineScoped: false,
  installPreReleaseVersion: false,
  isApplicationScoped: true,
  downloadExtensionsLocally: true,
  donotVerifySignature: false,
  donotIncludePackAndDependencies: true,
  keepExisting: true,
  profileLocation: qr {
    scheme: 'file',
    authority: '',
    path: '/home/node/.vscode-server/extensions/extensions.json',
    query: '',
    fragment: '',
    _formatted: 'file:///home/node/.vscode-server/extensions/extensions.json',
    _fsPath: '/home/node/.vscode-server/extensions/extensions.json'
  },
  productVersion: { version: '1.108.2', date: '2026-01-21T13:52:09.270Z' }
}
[11:14:03] Installing the extension without checking dependencies and pack anthropic.claude-code
[76500 ms] [11:14:04] Extracted extension to file:///home/node/.vscode-server/extensions/anthropic.claude-code-2.1.32: anthropic.claude-code
[76503 ms] [11:14:04] Renamed to /home/node/.vscode-server/extensions/anthropic.claude-code-2.1.32
[76518 ms] [11:14:04] Extension installed successfully: anthropic.claude-code file:///home/node/.vscode-server/extensions/extensions.json
[76575 ms] Port forwarding 51958 > 37513 > 37513: Local close
[76577 ms] Port forwarding 51957 > 37513 > 37513: Local close
[76577 ms] Port forwarding connection from 51989 > 37513 > 37513 in the container.
[76578 ms] Start: Run in container: /home/node/.vscode-server/bin/c9d77990917f3102ada88be140d28b038d1dd7c7/node -e
[76579 ms] Port forwarding connection from 51990 > 37513 > 37513 in the container.
[76579 ms] Start: Run in container: /home/node/.vscode-server/bin/c9d77990917f3102ada88be140d28b038d1dd7c7/node -e
[76690 ms] Port forwarding 51990 > 37513 > 37513 stderr: Connection established
[76691 ms] Port forwarding 51989 > 37513 > 37513 stderr: Connection established
[82705 ms] Port forwarding 51989 > 37513 > 37513 stderr: Remote close
[82706 ms] Port forwarding 51990 > 37513 > 37513 stderr: Remote close
[82726 ms] Port forwarding 51989 > 37513 > 37513 terminated with code 0 and signal null.
[82727 ms] Port forwarding 51990 > 37513 > 37513 terminated with code 0 and signal null.

```
