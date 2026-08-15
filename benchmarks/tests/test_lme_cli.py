from longmemeval import __main__ as cli


def test_parse_ingest():
    args = cli.parse_args(["ingest", "--difficulty", "oracle", "--variant", "raw", "--limit", "2"])
    assert args.command == "ingest"
    assert args.difficulty == "oracle"
    assert args.variant == "raw"
    assert args.limit == 2
