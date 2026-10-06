Real, SFW character cards for the cross-app normalization tests (codec, SillyTavern and Lumiverse must agree on the content hash). Each card keeps its own license.

| slug | name | creator | URL | license | format | covers |
|---|---|---|---|---|---|---|
| piko | Piko | yaethi | https://realm.risuai.net/character/2c34c212-e429-49e7-9b1c-86e9675d82d3 | CC0 | PNG with `chara` (V2) and `ccv3` (V3) | V2 PNG, V3 PNG, 4 alternate_greetings, depth_prompt, CRLF, empty scenario |
| piko-json-v2 | Piko | yaethi | see piko | CC0 | V2 JSON | V2 JSON, same card as piko |
| piko-json-v3 | Piko | yaethi | see piko | CC0 | V3 JSON | V3 JSON, same card as piko |
| piko-v2-only-png | Piko | derived from Piko by Innkeeper | see piko | CC0 | PNG with `chara` only | V2-only PNG |
| piko-regex | Piko | derived from Piko by Innkeeper | see piko | CC0 | V3 JSON | `extensions.regex_scripts` |
| helka | Helka | yaethi | https://realm.risuai.net/character/9058ffb9-d2af-4705-9cd6-8082c6439176 | CC BY 4.0 | V2 JSON | V2 JSON, 2 alternate_greetings, depth_prompt, CRLF, empty system_prompt |
| helka-png | Helka | yaethi | see helka | CC BY 4.0 | PNG with `chara` and `ccv3` | same card as helka |
| ucat-practice | UKCAT/UCAT Practice (verbal reasoning) | valstrax001 | https://realm.risuai.net/character/9a463650-4c05-4803-bd6c-174f96ef4de7 | CC BY-NC-SA 4.0 | V3 JSON | lorebook (7 entries, some with empty keys), empty personality/scenario/mes_example, 1 RisuAI customScript |
| ucat-practice-png | UKCAT/UCAT Practice (verbal reasoning) | valstrax001 | see ucat-practice | CC BY-NC-SA 4.0 | PNG with `chara` and `ccv3` | same card as ucat-practice |
| starlight-academy | Starlight Academy | qwer1029 | https://realm.risuai.net/character/e12f7dfaf462b1796438c690eab4d20e5df439120836692492d34a3e40cfbff0 | CC BY-NC-SA 4.0 | V2 JSON | lorebook (3 constant entries, empty keys), 2 alternate_greetings |
| firebird-ouroboros | Firebird Ouroboros | sjs1830 (card `creator`: Paradiso) | https://realm.risuai.net/character/e443839b-1ab3-49a2-94ed-28bbe3c06a21 | CC BY-SA 4.0 | CharX (`card.json`, `module.risum`, icon asset) | CharX, lorebook (57 entries incl. folder entries), 6 alternate_greetings, Korean and English text |
| crossroads-hearth | Crossroads Hearth | yaminabe | https://realm.risuai.net/character/93998a69e61f57fc86f26b1991664b71d6cb77a60088796f62a17e190ccb1cdb | CC BY-NC 4.0 | V3 JSON | 12 assets with `risustored:` URIs, 2 RisuAI customScripts, lorebook (2 entries), empty scenario |
| cook-book | Cook book | valstrax001 | https://realm.risuai.net/character/233d799e577c33954e0a587d85918c0b7709e8f7f5168438b4f4085d7431b89b | CC BY-NC 4.0 | V2 JSON | minimal card, empty personality/scenario/mes_example |

RisuRealm files came from `https://realm.risuai.net/api/v1/download/<format>/<id>`; licenses are the ones the RisuRealm page shows.

`derive.py` regenerates the two derived cards from Piko.

Known export inconsistencies between files of the same card:

- piko's PNG has creator "Yaethi"; its JSON files have "yaethi".
- ucat-practice's PNG has an empty creator; its JSON has "valstrax001".
