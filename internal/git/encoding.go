package git

/*
#cgo darwin LDFLAGS: -liconv
#include <git2.h>
#include <git2/sys/filter.h>
#include <git2/sys/errors.h>
#include <iconv.h>
#include <errno.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>

// Callback intent is scoped to a locked native call and its repository, not
// configuration. Other handles/threads and nested calls retain their intent.
typedef struct {
    const git_repository *repo;
} task_encoding_comparison_context;
static _Thread_local task_encoding_comparison_context task_encoding_context;
static task_encoding_comparison_context task_encoding_comparison_begin(const git_repository *repo) {
    task_encoding_comparison_context previous = task_encoding_context;
    task_encoding_context.repo = repo;
    return previous;
}
static void task_encoding_comparison_end(task_encoding_comparison_context previous) {
    task_encoding_context = previous;
}
static int task_encoding_comparing(const git_repository *repo) {
    return repo && task_encoding_context.repo == repo;
}

// Payloads belong to filter lists, streams own their buffers, and iconv
// descriptors belong to one call. Registration happens only at initialization.
typedef struct {
    char *encoding;
    char *path;
    int roundtrip, strict_clean;
} task_encoding_payload;

typedef struct {
    git_writestream parent;
    git_writestream *next;
    const task_encoding_payload *payload;
    char *input;
    size_t size, capacity;
    int clean, failed;
} task_encoding_stream;

static int task_encoding_error(const task_encoding_payload *p, const char *message) {
    git_error_set(GIT_ERROR_FILTER, "working-tree-encoding '%s' for '%s': %s",
                  p->encoding, p->path, message);
    return -1;
}

static int task_encoding_oom(void) {
    git_error_set_oom();
    return -1;
}

static int task_encoding_utf(const char *name, const char *suffix) {
    if (strncasecmp(name, "UTF", 3)) return 0;
    name += 3;
    if (*name == '-') name++;
    return !strcasecmp(name, suffix);
}

static int task_encoding_space(unsigned char c) {
    return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f';
}

// Match Git's first case-insensitive substring and delimiter checks, without
// conflating aliases: SHIFT-JIS and SHIFT_JIS are different config tokens.
static int task_encoding_roundtrip(const char *list, const char *name) {
    size_t n = strlen(name);
    for (const char *p = list; *p; p++) {
        if (!strncasecmp(p, name, n))
            return (p == list || p[-1] == ',' || task_encoding_space(p[-1])) &&
                   (!p[n] || p[n] == ',' || task_encoding_space(p[n]));
    }
    return 0;
}

static void task_encoding_cleanup(git_filter *self, void *payload) {
    task_encoding_payload *p = payload;
    if (!p) return;
    free(p->encoding);
    free(p->path);
    free(p);
}

static int task_encoding_payload_new(void **out, git_repository *repo,
                                     const char *path, const char *attribute) {
    git_config *config = NULL;
    git_config_entry *setting = NULL;
    const char *roundtrip = "SHIFT-JIS";
    task_encoding_payload *p;
    int error;
    *out = NULL;
    if (GIT_ATTR_IS_UNSPECIFIED(attribute)) return GIT_PASSTHROUGH;
    // Stable libgit2 collapses an empty assignment into boolean true. Reject
    // both representations consistently instead of guessing or bypassing a
    // requested conversion. Explicit UTF-8 and unspecified remain bypasses.
    if (!GIT_ATTR_HAS_VALUE(attribute) || !*attribute) {
        git_error_set(GIT_ERROR_FILTER,
            "working-tree-encoding for '%s': true/false and empty assignments are unsupported; "
            "use working-tree-encoding=UTF-8 or !working-tree-encoding to disable conversion", path);
        return -1;
    }
    if (task_encoding_utf(attribute, "8")) return GIT_PASSTHROUGH;
    p = calloc(1, sizeof(*p));
    if (!p) return task_encoding_oom();
    p->encoding = strdup(attribute);
    p->path = strdup(path ? path : "");
    if (!p->encoding || !p->path) {
        task_encoding_cleanup(NULL, p);
        return task_encoding_oom();
    }
    p->strict_clean = !task_encoding_comparing(repo);
    error = git_repository_config_snapshot(&config, repo);
    if (!error) {
        error = git_config_get_entry(&setting, config, "core.checkroundtripencoding");
        if (error == GIT_ENOTFOUND) {
            git_error_clear();
            error = 0;
        } else if (!error && !setting->value) {
            git_error_set(GIT_ERROR_CONFIG, "core.checkRoundtripEncoding requires a value");
            error = -1;
        } else if (!error) {
            roundtrip = setting->value;
        }
    }
    if (!error) p->roundtrip = task_encoding_roundtrip(roundtrip, attribute);
    git_config_entry_free(setting);
    git_config_free(config);
    if (error) { task_encoding_cleanup(NULL, p); return error; }
    *out = p;
    return 0;
}

static int task_encoding_check(git_filter *self, void **payload,
                               const git_filter_source *source, const char **attrs) {
    uint16_t mode = git_filter_source_filemode(source);
    if (mode && mode != GIT_FILEMODE_BLOB && mode != GIT_FILEMODE_BLOB_EXECUTABLE)
        return GIT_PASSTHROUGH;
    int error = task_encoding_payload_new(payload, git_filter_source_repo(source),
                                         git_filter_source_path(source), attrs[0]);
    if (!error && (git_filter_source_flags(source) & GIT_FILTER_ALLOW_UNSAFE))
        ((task_encoding_payload *)*payload)->strict_clean = 0;
    return error;
}

static int task_encoding_bom(const char *input, size_t size, int width) {
    if (width == 2)
        return size >= 2 && (!memcmp(input, "\xff\xfe", 2) || !memcmp(input, "\xfe\xff", 2));
    return size >= 4 && (!memcmp(input, "\xff\xfe\0\0", 4) || !memcmp(input, "\0\0\xfe\xff", 4));
}

static int task_encoding_validate_bom(const task_encoding_payload *p, const char *input, size_t size) {
    const char *enc = p->encoding;
    if (((task_encoding_utf(enc, "16LE") || task_encoding_utf(enc, "16BE")) && task_encoding_bom(input, size, 2)) ||
        ((task_encoding_utf(enc, "32LE") || task_encoding_utf(enc, "32BE")) && task_encoding_bom(input, size, 4)))
        return task_encoding_error(p, "BOM is prohibited");
    if ((task_encoding_utf(enc, "16") && !task_encoding_bom(input, size, 2)) ||
        (task_encoding_utf(enc, "32") && !task_encoding_bom(input, size, 4)))
        return task_encoding_error(p, "BOM is required");
    return 0;
}

static const char *task_encoding_fallback(const char *name) {
    if (task_encoding_utf(name, "8")) return "UTF-8";
    if (!strcasecmp(name, "latin-1")) return "ISO-8859-1";
    return name;
}

// Some system iconv implementations accept historical UTF-8 outside Unicode's
// scalar range even with strict conversion enabled. Keep the UTF-8 boundary
// valid independently of that host behavior; noncharacters remain valid.
static int task_encoding_valid_utf8(const char *data, size_t size) {
    const unsigned char *bytes = (const unsigned char *)data;
    size_t i = 0;
    while (i < size) {
        unsigned char lead = bytes[i++], low = 0x80, high = 0xbf;
        size_t continuation;
        if (lead <= 0x7f) continue;
        if (lead >= 0xc2 && lead <= 0xdf) continuation = 1;
        else if (lead >= 0xe0 && lead <= 0xef) {
            continuation = 2;
            if (lead == 0xe0) low = 0xa0;
            if (lead == 0xed) high = 0x9f;
        } else if (lead >= 0xf0 && lead <= 0xf4) {
            continuation = 3;
            if (lead == 0xf0) low = 0x90;
            if (lead == 0xf4) high = 0x8f;
        } else return 0;
        if (continuation > size - i || bytes[i] < low || bytes[i] > high) return 0;
        i++;
        while (--continuation) {
            if (bytes[i] < 0x80 || bytes[i] > 0xbf) return 0;
            i++;
        }
    }
    return 1;
}

// Buffers allocated here are always freed here, never passed off as git_buf
// allocations. Use the public writestream ABI to deliver converted bytes.
static int task_encoding_convert(char **output, size_t *output_size,
                                  const task_encoding_payload *p, const char *input,
                                  size_t size, int clean) {
    const char *from = clean ? p->encoding : "UTF-8";
    const char *to = clean ? "UTF-8" : p->encoding;
    const char *bom = NULL;
    size_t prefix = 0, capacity, remaining = size, used;
    char *buffer, *in = (char *)input, *out;
    iconv_t converter;
    *output = NULL;
    *output_size = 0;
    // Git bypasses empty content even for an unknown encoding.
    if (!size) return 0;
    if (clean && task_encoding_validate_bom(p, input, size)) return -1;
    if (!clean && !task_encoding_valid_utf8(input, size))
        return task_encoding_error(p, "invalid UTF-8 input for conversion");
    if (task_encoding_utf(from, "16LE-BOM")) from = "UTF-16";
    if (task_encoding_utf(to, "16LE-BOM")) {
        to = "UTF-16LE"; bom = "\xff\xfe"; prefix = 2;
    } else if (task_encoding_utf(to, "16BE-BOM")) {
        to = "UTF-16BE"; bom = "\xfe\xff"; prefix = 2;
    }
    converter = iconv_open(to, from);
    if (converter == (iconv_t)-1)
        converter = iconv_open(task_encoding_fallback(to), task_encoding_fallback(from));
    if (converter == (iconv_t)-1)
        return task_encoding_error(p, "host iconv does not support this conversion");
#ifdef __APPLE__
    // Apple's public iconv extension makes unmappable output an EILSEQ error
    // instead of silently substituting a replacement character.
    int invalid_is_error = 1;
    if (iconvctl(converter, ICONV_SET_ILSEQ_INVALID, &invalid_is_error)) {
        iconv_close(converter);
        return task_encoding_error(p, "cannot enable strict host iconv conversion");
    }
#endif
    if (size > SIZE_MAX - 32) { iconv_close(converter); return task_encoding_oom(); }
    capacity = size + 32;
    buffer = malloc(capacity);
    if (!buffer) { iconv_close(converter); return task_encoding_oom(); }
    if (prefix) memcpy(buffer, bom, prefix);
    used = prefix;
    for (;;) {
        size_t available = capacity - used;
        out = buffer + used;
        // macOS system libiconv and Linux libc both take char **. Like Git,
        // do not append an iconv reset sequence to stateful encodings.
        size_t count = iconv(converter, &in, &remaining, &out, &available);
        used = out - buffer;
        if (count != (size_t)-1) {
            if (count) {
                free(buffer); iconv_close(converter);
                return task_encoding_error(p, "non-reversible host iconv conversion");
            }
            break;
        }
        if (errno != E2BIG) {
            free(buffer); iconv_close(converter);
            return task_encoding_error(p, "invalid or unrepresentable input for conversion");
        }
        if (capacity > SIZE_MAX / 2) {
            free(buffer); iconv_close(converter); return task_encoding_oom();
        }
        capacity *= 2;
        char *grown = realloc(buffer, capacity);
        if (!grown) { free(buffer); iconv_close(converter); return task_encoding_oom(); }
        buffer = grown;
    }
    iconv_close(converter);
    if (clean && !task_encoding_valid_utf8(buffer, used)) {
        free(buffer);
        return task_encoding_error(p, "invalid Unicode scalar in converted content");
    }
    if (clean && p->strict_clean && p->roundtrip) {
        char *reverse = NULL;
        size_t reverse_size = 0;
        int error = task_encoding_convert(&reverse, &reverse_size, p, buffer, used, 0);
        if (!error && (reverse_size != size || memcmp(reverse, input, size)))
            error = task_encoding_error(p, "roundtrip conversion is not byte-identical");
        free(reverse);
        if (error) { free(buffer); return error; }
    }
    *output = buffer;
    *output_size = used;
    return 0;
}

static int task_encoding_write(git_writestream *stream, const char *buffer, size_t len) {
    task_encoding_stream *s = (task_encoding_stream *)stream;
    if (s->failed) return -1;
    if (!len) return 0;
    if (len > SIZE_MAX - s->size) { s->failed = 1; return task_encoding_oom(); }
    size_t required = s->size + len;
    if (required > s->capacity) {
        size_t capacity = required <= SIZE_MAX / 2 ? required * 2 : required;
        char *grown = realloc(s->input, capacity);
        if (!grown) { s->failed = 1; return task_encoding_oom(); }
        s->input = grown;
        s->capacity = capacity;
    }
    memcpy(s->input + s->size, buffer, len);
    s->size += len;
    return 0;
}

static int task_encoding_close(git_writestream *stream) {
    task_encoding_stream *s = (task_encoding_stream *)stream;
    char *output = NULL;
    size_t size = 0;
    // libgit2 closes streams even after a failed write. Never forward a
    // successfully converted prefix of a buffer whose accumulation failed.
    if (s->failed) return -1;
    int error = task_encoding_convert(&output, &size, s->payload, s->input, s->size, s->clean);
    if (error && s->clean && !s->payload->strict_clean) {
        const git_error *failure = git_error_last();
        // Git comparison-only conversion retains the original bytes on a
        // conversion error. Never hide allocation or configuration failures.
        if (failure && failure->klass == GIT_ERROR_FILTER) {
            git_error_clear();
            error = s->size ? s->next->write(s->next, s->input, s->size) : 0;
        }
    } else if (!error && size) {
        error = s->next->write(s->next, output, size);
    }
    free(output);
    if (!error) error = s->next->close(s->next);
    return error;
}

static void task_encoding_stream_free(git_writestream *stream) {
    task_encoding_stream *s = (task_encoding_stream *)stream;
    free(s->input);
    free(s);
}

static int task_encoding_stream_new(git_writestream **out, git_filter *self,
                                    void **payload, const git_filter_source *source,
                                    git_writestream *next) {
    task_encoding_stream *s = calloc(1, sizeof(*s));
    if (!s) return task_encoding_oom();
    s->parent.write = task_encoding_write;
    s->parent.close = task_encoding_close;
    s->parent.free = task_encoding_stream_free;
    s->next = next;
    s->payload = *payload;
    s->clean = git_filter_source_mode(source) == GIT_FILTER_TO_ODB;
    *out = &s->parent;
    return 0;
}

static git_filter task_encoding_filter = {
    .version = GIT_FILTER_VERSION,
    .attributes = "working-tree-encoding",
    .check = task_encoding_check,
    .stream = task_encoding_stream_new,
    .cleanup = task_encoding_cleanup
};

static int task_encoding_register(void) {
    // Smudge: native CRLF, native ident, encoding; clean reverses this.
    return git_filter_register("tasktool-working-tree-encoding", &task_encoding_filter, 150);
}

static int task_encoding_validate(git_repository *repo, const char *path,
                                  const char *input, size_t size, int clean, int index_only) {
    const char *attribute = NULL;
    void *payload = NULL;
    char *output = NULL;
    size_t output_size = 0;
    int error = git_attr_get(&attribute, repo, index_only ? GIT_ATTR_CHECK_INDEX_ONLY :
                             GIT_ATTR_CHECK_FILE_THEN_INDEX, path, "working-tree-encoding");
    if (error) return error;
    error = task_encoding_payload_new(&payload, repo, path, attribute);
    if (error == GIT_PASSTHROUGH) return 0;
    if (!error) error = task_encoding_convert(&output, &output_size, payload, input, size, clean);
    if (error && payload && clean && !((task_encoding_payload *)payload)->strict_clean) {
        const git_error *failure = git_error_last();
        if (failure && failure->klass == GIT_ERROR_FILTER) {
            git_error_clear();
            error = 0;
        }
    }
    free(output);
    task_encoding_cleanup(NULL, payload);
    return error;
}
*/
import "C"

import "unsafe"

// initializeEncodingFilter must run once, on the initialization OS thread,
// immediately after git_libgit2_init and before publishing any native handles.
func initializeEncodingFilter() error {
	return nativeError("register working-tree-encoding filter", C.task_encoding_register())
}

// encodingComparison scopes comparison-only clean conversions to this handle
// and locked OS thread. Checkout/merge smudge remains strict. Never use this
// around calls which write cleaned worktree content into the object database.
func encodingComparison(repo *C.git_repository) func() {
	previous := C.task_encoding_comparison_begin(repo)
	return func() { C.task_encoding_comparison_end(previous) }
}

// validateEncodingContent performs no writes. Callers hold the repository lock
// and OS thread, select the operation's attribute index/configuration, and pass
// only regular-file content. A whole operation must preflight every affected
// file before it writes any objects, files, indexes, registrations, or refs.
// Clean writes require configured byte-for-byte roundtrips. Callers explicitly
// scope comparison-only reads with encodingComparison, as the public native
// source flags do not distinguish every checkout comparison from a write.
func validateEncodingContent(repo *C.git_repository, path string, data []byte, clean, indexOnly bool) error {
	p, err := cstring(path)
	if err != nil {
		return err
	}
	defer free(p)
	var input *C.char
	if len(data) != 0 {
		input = (*C.char)(unsafe.Pointer(&data[0]))
	}
	var cleanFlag, indexFlag C.int
	if clean {
		cleanFlag = 1
	}
	if indexOnly {
		indexFlag = 1
	}
	return nativeError("preflight working-tree-encoding", C.task_encoding_validate(repo, p, input, C.size_t(len(data)), cleanFlag, indexFlag))
}
