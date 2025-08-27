# package-spec-schema

The repository contains [JSON Schema] specifications generated
from [elastic/package-spec]. Each [package-spec release] has its own directory.

## Schema Types

Each directory contains two schema formats:

- **Standard JSON schemas** (`jsonschema/`) - Multi-file schemas with [remote references]
- **IDE bundles** (`bundles/`) - Single-file schemas with embedded dependencies for better [IDE support]

The bundles resolve all remote references and
convert [compound schema documents] to standard `$defs` for IDE compatibility.

[JSON Schema]: https://json-schema.org/
[elastic/package-spec]: https://github.com/elastic/package-spec
[package-spec release]: https://github.com/elastic/package-spec/releases
[remote references]: https://json-schema.org/understanding-json-schema/structuring#dollarref
[IDE support]: https://json-schema.org/blog/posts/bundling-json-schema-compound-documents
[compound schema documents]: https://json-schema.org/understanding-json-schema/structuring#bundling

## License

The generated schemas inherit the same license as the source schemas
in [elastic/package-spec]. See the [LICENSE.txt] file.

The source code for generating the schemas (in [.generate/]) is licensed under
the [Apache 2.0 License].

[LICENSE.txt]: LICENSE.txt
[.generate/]: .generate/
[Apache 2.0 License]: .generate/LICENSE.txt

## Resources

- [Package Spec Documentation](https://github.com/elastic/package-spec)
- [JSON Schema Bundling Guide](https://json-schema.org/blog/posts/bundling-json-schema-compound-documents)
- [JSON Schema Specification](https://json-schema.org/specification)
 