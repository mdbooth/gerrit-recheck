# gerrit-recheck

A tool to automatically recheck a change in OpenStack CI if it fails.

This tool should not exist, but it does and it's useful. Make of that what you will.

## Gerrit authentication

The tool uses Gerrit's REST api, which on opendev uses HTTP basic authentication. To get an HTTP password you need to generate one.
When logged in to Gerrit, go to Settings->HTTP Credentials, and click `GENERATE NEW PASSWORD`.

The tool reads the password from stdin when invoked.
If you store your Gerrit credentials locally, please store them carefully, for example using a keystore with a master password.
Alternatively generate a new password from the gerrit web UI for each invocation.

## Building

```
make
```

## Usage

To automatically recheck change `https://review.opendev.org/c/openstack/openstacksdk/+/763121/`, do:

```
./gerrit-recheck -u MatthewBooth 763121
```

You can also specify multiple reviews:

```
./gerrit-recheck -u MatthewBooth 763121 763122 763123
```

The command will prompt for a HTTP password. You can use a utility like `keyring` to store and provide this automatically:

```
keyring get gerrit-recheck MatthewBooth | ./gerrit-recheck -u MatthewBooth 763121
```

> [!NOTE]
> You can set the password using e.g. `keyring set gerrit-review MatthewBooth <password>`

## Behaviour

The tool looks for a negative `Verified` vote from Zuul. If it finds one, it looks for a recheck comment dated later than the negative vote. If it doesn't find one, it adds one.

The tool polls Gerrit every 30 minutes.

The tool exits when Zuul gives a +2 `Verified` vote.
