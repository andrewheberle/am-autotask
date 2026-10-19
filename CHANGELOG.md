# Changelog

## [0.10.0](https://github.com/andrewheberle/am-autotask/compare/v0.9.0...v0.10.0) (2026-10-19)


### Features

* run multiple replicas in Kubernetes with leader election ([#38](https://github.com/andrewheberle/am-autotask/issues/38)) ([da71569](https://github.com/andrewheberle/am-autotask/commit/da715698b2fc586b9b5a12f4103503982b835454))

## [0.9.0](https://github.com/andrewheberle/am-autotask/compare/v0.8.0...v0.9.0) (2026-10-19)


### Features

* clearer default ticket titles, note titles and times ([#36](https://github.com/andrewheberle/am-autotask/issues/36)) ([9b7bd66](https://github.com/andrewheberle/am-autotask/commit/9b7bd664fc8a848aca598ee5cdea15fa41aa5532))
* **examples:** add Grafana dashboard ([#33](https://github.com/andrewheberle/am-autotask/issues/33)) ([f1ad931](https://github.com/andrewheberle/am-autotask/commit/f1ad931a07ff5ca20aec035743296f6b99c58574))
* let operators override ticket wording with Go templates ([#35](https://github.com/andrewheberle/am-autotask/issues/35)) ([66fd390](https://github.com/andrewheberle/am-autotask/commit/66fd3901ffc901721aae55870d3c1535cfeafd51))
* link recurring tickets through Autotask problem tickets ([#37](https://github.com/andrewheberle/am-autotask/issues/37)) ([9532328](https://github.com/andrewheberle/am-autotask/commit/9532328f91102484d40578b04fa00139b8839921))

## [0.8.0](https://github.com/andrewheberle/am-autotask/compare/v0.7.0...v0.8.0) (2026-10-19)


### Features

* set the work type of new tickets ([#29](https://github.com/andrewheberle/am-autotask/issues/29)) ([5311d9f](https://github.com/andrewheberle/am-autotask/commit/5311d9f8124c58286862ee580c6231b8ec254cf8))

## [0.7.0](https://github.com/andrewheberle/am-autotask/compare/v0.6.0...v0.7.0) (2026-10-19)


### Features

* let ticket priority follow alert group severity ([#27](https://github.com/andrewheberle/am-autotask/issues/27)) ([e3549d4](https://github.com/andrewheberle/am-autotask/commit/e3549d49807ab0ed8cbf1feaa5d3f899ce534613))

## [0.6.0](https://github.com/andrewheberle/am-autotask/compare/v0.5.0...v0.6.0) (2026-10-19)


### Features

* link alert runbooks in ticket descriptions and notes ([#23](https://github.com/andrewheberle/am-autotask/issues/23)) ([2bd6b70](https://github.com/andrewheberle/am-autotask/commit/2bd6b70b4a4c2566f85b7439f64f58e0ab02d7b1))


### Bug Fixes

* send company ID 0 when creating tickets ([#25](https://github.com/andrewheberle/am-autotask/issues/25)) ([0ed0e4e](https://github.com/andrewheberle/am-autotask/commit/0ed0e4e203298eca201fa9020544da72bcce986e))

## [0.5.0](https://github.com/andrewheberle/am-autotask/compare/v0.4.0...v0.5.0) (2026-10-18)


### ⚠ BREAKING CHANGES

* run the receiver as the serve subcommand ([#16](https://github.com/andrewheberle/am-autotask/issues/16))

### Features

* add lookup command for Autotask picklist and company IDs ([#18](https://github.com/andrewheberle/am-autotask/issues/18)) ([2d87dd6](https://github.com/andrewheberle/am-autotask/commit/2d87dd64708d6047700255c9969c3d5df2ac46a8))
* list valid values when a configured ID is rejected ([#19](https://github.com/andrewheberle/am-autotask/issues/19)) ([d00a7db](https://github.com/andrewheberle/am-autotask/commit/d00a7dbceea0e5313d2426154f15d35d034b7e10))
* run the receiver as the serve subcommand ([#16](https://github.com/andrewheberle/am-autotask/issues/16)) ([4c2b553](https://github.com/andrewheberle/am-autotask/commit/4c2b5530ec785b83cd2f51a910a1ff692d8579d6))


### Bug Fixes

* allow your own company (ID 0) as the catch-all company ([#20](https://github.com/andrewheberle/am-autotask/issues/20)) ([a10e967](https://github.com/andrewheberle/am-autotask/commit/a10e96705326d6a1d955d77ea925f7c48d83cc98))
* print command errors as readable text ([#21](https://github.com/andrewheberle/am-autotask/issues/21)) ([10cd281](https://github.com/andrewheberle/am-autotask/commit/10cd281676c9214286d3c7186cfb6ad30f4da678))

## [0.4.0](https://github.com/andrewheberle/am-autotask/compare/v0.3.0...v0.4.0) (2026-10-17)


### Features

* add OpenTelemetry tracing of notifications ([#13](https://github.com/andrewheberle/am-autotask/issues/13)) ([0f47630](https://github.com/andrewheberle/am-autotask/commit/0f47630cac40b9a680e5e0db0eceab18d8a93c1f))

## [0.3.0](https://github.com/andrewheberle/am-autotask/compare/v0.2.0...v0.3.0) (2026-10-17)


### Features

* add catch-all, readiness, selector validity and latency metrics ([#10](https://github.com/andrewheberle/am-autotask/issues/10)) ([a5810d4](https://github.com/andrewheberle/am-autotask/commit/a5810d4b443a4c257e7976d2f06c102956caa6d8))

## [0.2.0](https://github.com/andrewheberle/am-autotask/compare/v0.1.0...v0.2.0) (2026-10-17)


### Features

* hand alerts over from orphaned tickets ([#7](https://github.com/andrewheberle/am-autotask/issues/7)) ([071977d](https://github.com/andrewheberle/am-autotask/commit/071977d88aa41580d7b700f9260a969d868f8e22))

## [0.1.0](https://github.com/andrewheberle/am-autotask/compare/v0.0.1...v0.1.0) (2026-10-17)


### Features

* initial receiver implementation ([#1](https://github.com/andrewheberle/am-autotask/issues/1)) ([9becc70](https://github.com/andrewheberle/am-autotask/commit/9becc7074ff151e4ca99b1eb925c31ce3d20c4b4))


### Bug Fixes

* **dockerfile:** add expose line for port ([#3](https://github.com/andrewheberle/am-autotask/issues/3)) ([3e99bad](https://github.com/andrewheberle/am-autotask/commit/3e99baddf26e7ff135bb292d60691f03d8cf7c40))
