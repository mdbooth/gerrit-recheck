# gerrit-recheck

A tool to automatically recheck a change in OpenStack CI if it fails.

This tool should not exist, but it does and it's useful. Make of that what you will.

## Gerrit authentication

The tool uses Gerrit's REST api, which on opendev uses HTTP basic authentication. To get an HTTP password you need to generate one.
When logged in to Gerrit, go to Settings->HTTP Credentials, and click `GENERATE NEW PASSWORD`.

The tools reads the password from a `.netrc` file. This file should be located at `~/.netrc` with permissions 0600. The following is an example of a correct file:

```
machine review.opendev.org login MatthewBooth password SooperS3cr3t
```

## Building

```
make
```

## Usage

To automatically recheck change `https://review.opendev.org/c/openstack/openstacksdk/+/763121/`, do:

```
./gerrit-recheck 763121
```

## Behaviour

The tool looks for a negative `Verified` vote from Zuul. If it finds one, it looks for a recheck comment dated later than the negative vote. If it doesn't find one, it adds one.

The tool polls gerrit every 30 minutes.

The tool exits when Zuul gives a +2 `Verified` vote.
