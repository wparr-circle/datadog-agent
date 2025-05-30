from __future__ import annotations

from collections import Counter
from functools import lru_cache
from typing import Any


@lru_cache
def read_owners(owners_file: str, remove_default_pattern=False) -> Any:
    """
    - remove_default_pattern: If True, will remove the '*' entry
    """
    from codeowners import CodeOwners

    with open(owners_file) as f:
        lines = f.readlines()

        if remove_default_pattern:
            try:
                is_default_pattern = [bool(line.strip() and line.strip().split()[0] != '*' for line in lines)]
                assert sum(is_default_pattern) == 1
            except IndexError as e:
                e.add_note("Exactly one default pattern '*' should be present in {owners_file}")
                raise

            index_default_pattern = is_default_pattern.index(True)
            lines = lines[:index_default_pattern] + lines[index_default_pattern + 1 :]

        return CodeOwners('\n'.join(lines))


def search_owners(search: str, owners_file: str) -> list[str]:
    parsed_owners = read_owners(owners_file)
    # owners.of returns a list in the form: [('TEAM', '@DataDog/agent-delivery')]
    return [owner[1] for owner in parsed_owners.of(search)]


def list_owners(owners_file=".github/CODEOWNERS"):
    owners = read_owners(owners_file)
    for path in owners.paths:
        for team in path[2]:
            yield team[1].casefold().replace('@datadog/', '')


def most_frequent_agent_team(teams):
    agent_teams = list(list_owners())
    c = Counter(teams)
    for team in c.most_common():
        if team[0] in agent_teams:
            return team[0]
    return 'triage'
