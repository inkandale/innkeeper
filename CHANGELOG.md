# Changelog

## [0.3.0](https://github.com/davidgibbons/innkeeper/compare/v0.2.0...v0.3.0) (2026-10-05)


### Features

* **api:** start an import from a target app through POST /imports and MCP's start_import ([8651a69](https://github.com/davidgibbons/innkeeper/commit/8651a69b5553677f8c6f9b4ddbca3b832f988b0d))
* bundle the SillyTavern plugin that returns avatars and world links for imports ([0fed79f](https://github.com/davidgibbons/innkeeper/commit/0fed79f30fb4f3e57b1c09e51b5427f813fec705))
* **cli:** show import outcomes when watching a job, and document importing an app's library ([b66ae48](https://github.com/davidgibbons/innkeeper/commit/b66ae482f38c46736ebffcf2ea290c2a87433178))
* import an app's characters and lorebooks into the library ([3f6402a](https://github.com/davidgibbons/innkeeper/commit/3f6402ac93b93c499a6428c94e4d1f72b0a52a0e))
* **protocol:** let target.get return the avatar and the card's lorebook links ([57d2440](https://github.com/davidgibbons/innkeeper/commit/57d2440e3ff2796fcde1394d37a70ebd6eec1b97))
* **targets:** import an app's characters and lorebooks into the library, bound to their app items ([4a7faee](https://github.com/davidgibbons/innkeeper/commit/4a7faeec663c72c8084a1d4c5a706fc188a2aa34))
* **targets:** record the lorebooks the app links to a card when binding it ([4d70c44](https://github.com/davidgibbons/innkeeper/commit/4d70c44a2d687e14c626bb8240ce3794f95661a8))


### Bug Fixes

* **targets:** report already_bound when another import child bound the item first ([5c859ab](https://github.com/davidgibbons/innkeeper/commit/5c859ab74ccbb49dc14a722f57f3389de26906ba))

## [0.2.0](https://github.com/davidgibbons/innkeeper/compare/v0.1.0...v0.2.0) (2026-10-05)


### Features

* **chart:** run the image matching the chart's appVersion by default ([3f0e8c2](https://github.com/davidgibbons/innkeeper/commit/3f0e8c23da70ae4772aa8e6ab5e3504850a8ced3))
