# Reactor

:>

Reactor monitors chats with your Telegram account, tracks reactions and positive replies, and forwards popular messages to your channel. A separate bot handles commands in monitored chats.

## Features

- Monitor any chat or channel for reactions
- Forward messages with enough replies/reactions to your channel
- Adapt thresholds per chat from recent message ratings
- Reply with `/r` to see a recorded message's rating and threshold

> Shared Telegram Stories are forwarded as Story references, so Telegram keeps their original authorship. They are not reposted as plain media because that would lose the Story's interactive elements.

## Installation

To install Reactor, you need to have [Go](https://golang.org/) installed on your system. Then, follow these steps:

1. Clone this repository: `git clone https://github.com/sleroq/reactor.git`
2. Change directory to the project folder: `cd reactor`
3. Create configuration file: `cp scripts/env.bash.example scripts/env.bash`
4. Start the bot: `./scripts/run.bash`

## Configuration

Copy `scripts/env.bash.example` to `scripts/env.bash` and fill in the required values:

```bash
export REACTOR_PHONE=""
export REACTOR_APP_ID=""
export REACTOR_APP_HASH=""
export REACTOR_COMMAND_BOT_TOKEN=""
export REACTOR_SESSION_DIR=./session
export REACTOR_CHAT_IDS="123123,23123"
export REACTOR_CHANNEL_ID=""
export REACTOR_CHANNEL_ACCESS_HASH=""
```

Get your Telegram API ID and hash from [my.telegram.org](https://my.telegram.org/apps). Create the command bot with @BotFather, set its token as `REACTOR_COMMAND_BOT_TOKEN`, and add it to every monitored chat. The phone-authenticated client monitors and forwards messages; the command bot responds to `/r` and `/help` (including `@BotUsername` mentions).

## Usage

- [How to not get banned?](https://github.com/gotd/td/blob/main/.github/SUPPORT.md#how-to-not-get-banned)
- <details>
    <summary>
      How do adaptive thresholds work?
    </summary>
    <code>
    export REACTOR_TEXT_THRESHOLD=31
    export REACTOR_PHOTO_THRESHOLD=23
    export REACTOR_FORWARD_THRESHOLD=23
    export REACTOR_TEXT_MAX_THRESHOLD=62
    export REACTOR_PHOTO_MAX_THRESHOLD=46
    export REACTOR_FORWARD_MAX_THRESHOLD=46
    export REACTOR_THRESHOLD_HISTORY_DAYS=7
    export REACTOR_THRESHOLD_MATURITY_HOURS=6
    export REACTOR_TARGET_FORWARDS_PER_DAY=3
    </code>

    Thresholds are recalculated hourly for each chat from mature messages in the history window. The minimum and maximum values bound each message category; the target controls the approximate number of forwards per day.
  </details>
- <details>
    <summary>
      Don't remove author for some channels/users
    </summary>
    <code>
    export REACTOR_NOQUOTE_WHITELIST="123123,233424"
    </code>
  </details>

## Licence

This project is licensed under the GPL-3.0-or-later - see the [LICENSE](./LICENSE) file for details.
