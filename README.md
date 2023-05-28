# Memoq

:>

Memoq is a user-bot that helps you discover the most popular memes and posts from any chat or channel. It tracks how many people react or reply to each message and sends the ones that reach a certain threshold to your channel. You can set the threshold and choose whether to use emoji or reply counts as criteria. With Memoq, you’ll never miss a viral meme again!

# Features

- Monitor any chat or channel for reactions
- Forward messages with enough replies/reactions to your channel
- Customize the threshold for forwarding messages
- Use emoji reactions or reply counts as criteria


## Installation
To install Memoq, you need to have [Go](https://golang.org/) installed on your system. Then, follow these steps:

1. Clone this repository: `git clone https://github.com/sleroq/memoq.git`
2. Change directory to the project folder: `cd memoq`
3. Create configuration file: `cp scripts/env.bash.example scripts/env.bash`
4. Start the bot: `./scripts/run.bash`

## Configuration

Before running the bot, you need to create a configuration file named `scripts/env.bash` in `scripts` folder as the executable. The configuration file should have the following variables:
```bash
export TG_PHONE=""
export LMEM_API_ID=""
export LMEM_API_HASH=""
export SESSION_FILE=./dist
export CHAT_ID=""
export CHANNEL_ID=""
export CHANNEL_ACCESS_HASH=""
```

You can obtain your Telegram API ID and API hash from [here](https://my.telegram.org/apps). You can get your Telegram chat/channel ID by from updates or by using other bots.

## Usage

- [How to not get banned?](https://github.com/gotd/td/blob/main/.github/SUPPORT.md#how-to-not-get-banned)

## Licence

This project is licensed under the GPL-3.0-or-later - see the [LICENSE](./LICENCE) file for details.