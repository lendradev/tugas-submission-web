// Logika aplikasi untuk Tugas Submission.
//
// Alpine.js build CSP tidak mendukung arrow function maupun akses global
// (Math, JSON, encodeURIComponent) di dalam ekspresi HTML. Karena itu seluruh
// logika berada di file ini; template hanya memanggil method yang sudah ada.

(function () {
  "use strict";

  var ROOT = "/api";

  function qs(sel) {
    return document.querySelector(sel);
  }

  function formatSize(bytes) {
    var n = Number(bytes) || 0;
    if (n < 1024) return n + " B";
    var units = ["KB", "MB", "GB"];
    var i = -1;
    do {
      n = n / 1024;
      i = i + 1;
    } while (n >= 1024 && i < units.length - 1);
    return (n >= 10 ? Math.round(n) : Math.round(n * 10) / 10) + " " + units[i];
  }

  // Ikon berkas memakai SVG inline, bukan emoji, supaya tampilan konsisten
  // di semua sistem (emoji sering tidak tersedia di server tanpa font emoji).
  var FILE_ICONS = {
    pdf: { color: "#d93025", shape: "M6 2h8l4 4v16H6z M14 2v4h4" },
    doc: { color: "#1a73e8", shape: "M6 2h8l4 4v16H6z M14 2v4h4" },
    sheet: { color: "#188038", shape: "M4 4h16v16H4z M4 9h16 M9 4v16" },
    slide: { color: "#e8710a", shape: "M3 4h18v12H3z M12 16v4 M8 20h8" },
    zip: { color: "#5f6368", shape: "M6 2h8l4 4v16H6z M10 6h2v2h-2z M10 10h2v2h-2z M10 14h2v2h-2z" },
    image: { color: "#9334e6", shape: "M3 5h18v14H3z M3 16l5-5 4 4 3-3 6 6" },
    code: { color: "#202124", shape: "M9 7l-5 5 5 5 M15 7l5 5-5 5" },
    folder: { color: "#fbbc04", shape: "M3 6h6l2 2h10v11H3z" },
    file: { color: "#5f6368", shape: "M6 2h8l4 4v16H6z M14 2v4h4" },
  };

  function iconKind(name, isDir) {
    if (isDir) return "folder";
    var ext = String(name || "").split(".").pop().toLowerCase();
    if (["pdf"].indexOf(ext) >= 0) return "pdf";
    if (["doc", "docx", "odt", "rtf"].indexOf(ext) >= 0) return "doc";
    if (["xls", "xlsx", "ods", "csv"].indexOf(ext) >= 0) return "sheet";
    if (["ppt", "pptx", "odp"].indexOf(ext) >= 0) return "slide";
    if (["zip", "rar", "7z", "tar", "gz", "bz2"].indexOf(ext) >= 0) return "zip";
    if (["png", "jpg", "jpeg", "gif", "webp", "svg", "bmp"].indexOf(ext) >= 0) return "image";
    if (["go", "py", "js", "ts", "html", "css", "rs", "java", "c", "cpp"].indexOf(ext) >= 0) return "code";
    return "file";
  }

  // Alpine build CSP melarang x-html, sehingga SVG ikon dikirim sebagai
  // data-URI lalu ditampilkan lewat atribut src.
  function svgDataURI(svg) {
    return "data:image/svg+xml;charset=utf-8," + encodeURIComponent(svg);
  }

  function fileIcon(name, isDir) {
    var kind = iconKind(name, isDir);
    var def = FILE_ICONS[kind] || FILE_ICONS.file;
    return svgDataURI(
      '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="' +
        def.color +
        '" stroke-width="1.6" stroke-linejoin="round" stroke-linecap="round">' +
        '<path d="' +
        def.shape +
        '"/></svg>'
    );
  }

  function formatTime(iso) {
    if (!iso) return "—";
    var d = new Date(iso);
    if (isNaN(d.getTime())) return "—";
    return d.toLocaleString("id-ID", {
      day: "2-digit",
      month: "2-digit",
      year: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
  }

  // request melakukan fetch JSON dengan header CSRF dan penanganan galat seragam.
  function request(url, options) {
    var opts = options || {};
    var headers = opts.headers || {};
    if (opts.csrf) headers["X-CSRF-Token"] = opts.csrf;
    if (opts.body && !(opts.body instanceof FormData)) {
      headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(opts.body);
    }
    return fetch(url, {
      method: opts.method || "GET",
      headers: headers,
      body: opts.body,
      credentials: "same-origin",
    }).then(function (res) {
      return res
        .json()
        .catch(function () {
          return {};
        })
        .then(function (data) {
          if (!res.ok) {
            var msg = data && data.error ? data.error : "Permintaan gagal (" + res.status + ")";
            throw new Error(msg);
          }
          return data;
        });
    });
  }

  // ------------------------------------------------------------------ files

  function filesApp() {
    return {
      user: JSON.parse(document.body.getAttribute("data-user") || "{}"),
      csrf: document.body.getAttribute("data-csrf") || "",
      maxUploadMB: Number(document.body.getAttribute("data-max-upload") || "25"),
      repoReady: document.body.getAttribute("data-repo-ready") === "1",

      items: [],
      filtered: [],
      path: "",
      query: "",
      selection: [],
      view: "list",
      loading: false,
      error: "",
      dragging: false,

      status: { pending: 0, next_push_at: "", last_push_at: "", last_error: "" },

      uploadOpen: false,
      uploadError: "",
      uploading: false,
      progress: 0,
      progressLabel: "",
      picks: [],

      renameOpen: false,
      renameValue: "",
      renameError: "",

      deleteOpen: false,
      deleteTarget: null,
      deleteError: "",

      init: function () {
        this.load();
        this.refreshStatus();
        var self = this;
        setInterval(function () {
          self.refreshStatus();
        }, 30000);
      },

      load: function () {
        var self = this;
        this.loading = true;
        this.error = "";
        var url = ROOT + "/list";
        if (this.path) url += "?path=" + encodeURIComponent(this.path);
        return request(url)
          .then(function (data) {
            self.items = data.items || [];
            self.applyFilter();
          })
          .catch(function (err) {
            self.error = err.message;
          })
          .finally(function () {
            self.loading = false;
          });
      },

      applyFilter: function () {
        var q = this.query.trim().toLowerCase();
        if (!q) {
          this.filtered = this.items;
          return;
        }
        this.filtered = this.items.filter(function (f) {
          return String(f.name).toLowerCase().indexOf(q) >= 0;
        });
      },

      refreshStatus: function () {
        var self = this;
        return request(ROOT + "/status")
          .then(function (data) {
            self.status = data;
            if (data.csrf) self.csrf = data.csrf;
          })
          .catch(function () {
            /* diamkan: polling status tidak boleh mengganggu */
          });
      },

      navigate: function (path) {
        this.path = path || "";
        this.selection = [];
        this.load();
      },

      openItem: function (f) {
        if (f.is_dir) {
          this.navigate(f.path);
        }
      },

      toggleView: function () {
        this.view = this.view === "list" ? "grid" : "list";
      },

      breadcrumbs: function () {
        if (!this.path) return [];
        var parts = this.path.split("/");
        var out = [];
        var acc = "";
        for (var i = 0; i < parts.length; i++) {
          acc = acc ? acc + "/" + parts[i] : parts[i];
          out.push({ name: parts[i], path: acc });
        }
        return out;
      },

      sizeLabel: formatSize,
      iconFor: fileIcon,
      // CSP build Alpine tidak menyediakan global encodeURIComponent,
      // sehingga URL unduhan dibuat di sini.
      downloadUrl: function (path) {
        return "/download?path=" + encodeURIComponent(path);
      },
      usedLabel: function () {
        var used = this.status.used_bytes || 0;
        var quota = this.status.quota_bytes || 0;
        return formatSize(used) + " / " + formatSize(quota);
      },
      nextPushLabel: function () {
        if (!this.status.next_push_at) return "—";
        return formatTime(this.status.next_push_at);
      },

      // ---------------------------------------------------------------- upload

      openUpload: function () {
        this.uploadOpen = true;
        this.uploadError = "";
        this.picks = [];
        this.progress = 0;
      },

      onDrop: function (event) {
        var self = this;
        this.dragging = false;
        if (event.dataTransfer && event.dataTransfer.files) {
          this.setPicks(event.dataTransfer.files);
          if (!this.uploadOpen) this.openUpload();
        }
      },

      onPick: function (event) {
        this.setPicks(event.target.files);
      },

      setPicks: function (fileList) {
        var picks = [];
        for (var i = 0; i < fileList.length; i++) {
          picks.push({ name: fileList[i].name, size: fileList[i].size, file: fileList[i] });
        }
        this.picks = picks;
      },

      doUpload: function () {
        var self = this;
        if (!this.picks.length) return;
        this.uploading = true;
        this.uploadError = "";
        this.progress = 0;
        this.progressLabel = "Mengunggah…";

        var form = new FormData();
        for (var i = 0; i < this.picks.length; i++) {
          form.append("files", this.picks[i].file, this.picks[i].name);
        }

        // Path folder dikirim sebagai query parameter: multipart Reader
        // bersifat streaming sehingga field tambahan bisa terpotong.
        var url = ROOT + "/upload";
        if (this.path) url += "?path=" + encodeURIComponent(this.path);

        var xhr = new XMLHttpRequest();
        xhr.open("POST", url, true);
        xhr.setRequestHeader("X-CSRF-Token", this.csrf);
        xhr.upload.onprogress = function (e) {
          if (e.lengthComputable) {
            self.progress = Math.round((e.loaded / e.total) * 100);
            self.progressLabel = "Mengunggah " + self.progress + "%";
          }
        };
        xhr.onload = function () {
          self.uploading = false;
          if (xhr.status >= 200 && xhr.status < 300) {
            self.uploadOpen = false;
            self.picks = [];
            self.progress = 0;
            self.load();
            self.refreshStatus();
          } else {
            var msg = "Upload gagal";
            try {
              msg = JSON.parse(xhr.responseText).error || msg;
            } catch (e) {
              /* biarkan pesan bawaan */
            }
            self.uploadError = msg;
          }
        };
        xhr.onerror = function () {
          self.uploading = false;
          self.uploadError = "Koneksi terputus saat mengunggah";
        };
        xhr.send(form);
      },

      // ---------------------------------------------------------------- rename

      startRename: function (f) {
        this.renameOpen = true;
        this.renameError = "";
        this.renameValue = f.name;
        this.renameTarget = f.path;
      },

      doRename: function () {
        var self = this;
        var dir = this.path ? this.path + "/" : "";
        var to = dir + this.renameValue;
        return request(ROOT + "/rename", {
          method: "POST",
          csrf: this.csrf,
          body: { from: this.renameTarget, to: to },
        })
          .then(function () {
            self.renameOpen = false;
            self.load();
            self.refreshStatus();
          })
          .catch(function (err) {
            self.renameError = err.message;
          });
      },

      // ---------------------------------------------------------------- delete

      askDelete: function (f) {
        this.deleteOpen = true;
        this.deleteError = "";
        this.deleteTarget = f;
      },

      doDelete: function () {
        var self = this;
        if (!this.deleteTarget) return;
        return request(ROOT + "/delete", {
          method: "POST",
          csrf: this.csrf,
          body: { paths: [this.deleteTarget.path] },
        })
          .then(function () {
            self.deleteOpen = false;
            self.deleteTarget = null;
            self.load();
            self.refreshStatus();
          })
          .catch(function (err) {
            self.deleteError = err.message;
          });
      },

      closeDialogs: function () {
        this.uploadOpen = false;
        this.renameOpen = false;
        this.deleteOpen = false;
      },
    };
  }

  // ------------------------------------------------------------------ admin

  function adminApp() {
    return {
      user: JSON.parse(document.body.getAttribute("data-user") || "{}"),
      csrf: document.body.getAttribute("data-csrf") || "",

      users: [],
      stats: { user_count: 0, file_count: 0, total_size: 0, pending: 0 },
      error: "",
      notice: "",

      form: { username: "", display_name: "", password: "", role: "user" },
      settings: {
        max_upload_size_mb: 25,
        total_quota_mb: 5120,
        max_files_per_request: 20,
        debounce_base_min: 5,
        debounce_min_min: 1,
        debounce_max_min: 30,
        burst_window_min: 15,
      },

      init: function () {
        this.loadUsers();
        this.loadSettings();
      },

      loadUsers: function () {
        var self = this;
        return request("/admin/users")
          .then(function (data) {
            self.users = data.users || [];
            self.stats = data.stats || self.stats;
          })
          .catch(function (err) {
            self.error = err.message;
          });
      },

      loadSettings: function () {
        var self = this;
        return request("/admin/settings")
          .then(function (data) {
            self.settings = data.settings || self.settings;
          })
          .catch(function (err) {
            self.error = err.message;
          });
      },

      sizeLabel: formatSize,

      createUser: function () {
        var self = this;
        this.error = "";
        this.notice = "";
        var form = new FormData();
        form.append("username", this.form.username);
        form.append("display_name", this.form.display_name);
        form.append("password", this.form.password);
        form.append("role", this.form.role);
        return request("/admin/users", { method: "POST", csrf: this.csrf, body: form })
          .then(function () {
            self.notice = "Akun " + self.form.username + " dibuat.";
            self.form.username = "";
            self.form.display_name = "";
            self.form.password = "";
            self.form.role = "user";
            self.loadUsers();
          })
          .catch(function (err) {
            self.error = err.message;
          });
      },

      setRole: function (u) {
        var self = this;
        var form = new FormData();
        form.append("role", u.role);
        return request("/admin/users/" + u.id + "/role", { method: "POST", csrf: this.csrf, body: form })
          .then(function () {
            self.notice = "Peran " + u.username + " diperbarui.";
          })
          .catch(function (err) {
            self.error = err.message;
            self.loadUsers();
          });
      },

      toggleActive: function (u) {
        var self = this;
        var form = new FormData();
        form.append("active", u.active ? "0" : "1");
        return request("/admin/users/" + u.id + "/active", { method: "POST", csrf: this.csrf, body: form })
          .then(function () {
            self.notice = "Status " + u.username + " diperbarui.";
            self.loadUsers();
          })
          .catch(function (err) {
            self.error = err.message;
          });
      },

      resetPassword: function (u) {
        var pw = window.prompt("Password baru untuk " + u.username + " (minimal 8 karakter):");
        if (!pw) return;
        var self = this;
        var form = new FormData();
        form.append("password", pw);
        return request("/admin/users/" + u.id + "/password", { method: "POST", csrf: this.csrf, body: form })
          .then(function () {
            self.notice = "Password " + u.username + " diganti.";
          })
          .catch(function (err) {
            self.error = err.message;
          });
      },

      forcePush: function (u) {
        var self = this;
        return request("/admin/users/" + u.id + "/push", { method: "POST", csrf: this.csrf })
          .then(function () {
            self.notice = "Sinkronisasi " + u.username + " dijalankan.";
          })
          .catch(function (err) {
            self.error = err.message;
          });
      },

      removeUser: function (u) {
        if (!window.confirm("Hapus akun " + u.username + " beserta seluruh berkasnya?")) return;
        var self = this;
        return request("/admin/users/" + u.id + "/delete", { method: "POST", csrf: this.csrf })
          .then(function () {
            self.notice = "Akun " + u.username + " dihapus.";
            self.loadUsers();
          })
          .catch(function (err) {
            self.error = err.message;
          });
      },

      saveSettings: function () {
        var self = this;
        this.error = "";
        this.notice = "";
        var form = new FormData();
        form.append("max_upload_size_mb", String(this.settings.max_upload_size_mb));
        form.append("total_quota_mb", String(this.settings.total_quota_mb));
        form.append("max_files_per_request", String(this.settings.max_files_per_request));
        form.append("debounce_base_min", String(this.settings.debounce_base_min));
        form.append("debounce_min_min", String(this.settings.debounce_min_min));
        form.append("debounce_max_min", String(this.settings.debounce_max_min));
        form.append("burst_window_min", String(this.settings.burst_window_min));
        return request("/admin/settings", { method: "POST", csrf: this.csrf, body: form })
          .then(function () {
            self.notice = "Pengaturan disimpan.";
          })
          .catch(function (err) {
            self.error = err.message;
          });
      },
    };
  }

  // Alpine.js build CSP hanya mengevaluasi ekspresi yang ada di dalam scope
  // element; `extras.scope` diabaikan evaluator CSP sehingga
  // `x-data="filesApp"` (provider Alpine.data) tidak bisa dipakai.
  //
  // Pola yang dipakai: state reaktif dibuat di app.js dan dibungkus sebagai
  // magic `$app`. Template memakai `x-data="{ s: $app }"` lalu memanggil
  // method lewat `s.namaMethod()` — MemberExpression + CallExpression
  // didukung parser CSP.
  //
  // Registrasi memakai event "alpine:init" yang di-dispatch Alpine sebelum
  // x-data dievaluasi. Karena itu app.js HARUS dimuat sebelum alpine.min.js.
  var filesState = null;
  var adminState = null;

  function ensureFilesState() {
    if (!filesState) filesState = filesApp();
    return filesState;
  }

  function ensureAdminState() {
    if (!adminState) adminState = adminApp();
    return adminState;
  }

  document.addEventListener("alpine:init", function () {
    window.Alpine.magic("app", function () {
      // Halaman menentukan halaman mana yang aktif lewat atribut body.
      var isAdmin = document.body.getAttribute("data-page") === "admin";
      return isAdmin ? ensureAdminState() : ensureFilesState();
    });
  });
})();
