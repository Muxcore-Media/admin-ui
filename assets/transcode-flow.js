(function () {
  "use strict";

  var STEP_DEFS = {
    "filter.skip_if_codec": {
      label: "Skip if codec",
      kind: "filter",
      defaults: { codecs: ["hevc", "h265"] },
      fields: [
        { key: "codecs", label: "Codecs (comma-separated)", type: "text", list: true },
      ],
    },
    "filter.skip_if_container": {
      label: "Skip if container",
      kind: "filter",
      defaults: { containers: ["mkv"] },
      fields: [
        { key: "containers", label: "Containers", type: "text", list: true },
      ],
    },
    "filter.min_size_mb": {
      label: "Min size (MB)",
      kind: "filter",
      defaults: { min_mb: 100 },
      fields: [{ key: "min_mb", label: "Minimum MB", type: "number" }],
    },
    "filter.max_size_mb": {
      label: "Max size (MB)",
      kind: "filter",
      defaults: { max_mb: 50000 },
      fields: [{ key: "max_mb", label: "Maximum MB", type: "number" }],
    },
    "filter.extension": {
      label: "Extension allowlist",
      kind: "filter",
      defaults: { extensions: ["mkv", "mp4"] },
      fields: [
        { key: "extensions", label: "Extensions", type: "text", list: true },
      ],
    },
    "filter.skip_if_output_exists": {
      label: "Skip if output exists",
      kind: "filter",
      defaults: { suffix: "-hevc" },
      fields: [{ key: "suffix", label: "Output suffix", type: "text" }],
    },
    "action.remux": {
      label: "Remux container",
      kind: "action",
      defaults: { container: "mp4" },
      fields: [{ key: "container", label: "Target container", type: "text" }],
    },
  };

  function uid(prefix) {
    return prefix + "_" + Math.random().toString(36).slice(2, 10);
  }

  function parseInit(root) {
    var raw = root.getAttribute("data-init") || "{}";
    try {
      return JSON.parse(raw);
    } catch (e) {
      return {};
    }
  }

  function listToText(v) {
    if (Array.isArray(v)) {
      return v.join(", ");
    }
    return String(v == null ? "" : v);
  }

  function textToList(v) {
    return String(v || "")
      .split(",")
      .map(function (s) {
        return s.trim();
      })
      .filter(Boolean);
  }

  function configToFields(stepType, config) {
    var def = STEP_DEFS[stepType];
    if (!def) {
      return config || {};
    }
    var out = {};
    def.fields.forEach(function (field) {
      if (field.list) {
        out[field.key] = textToList(config && config[field.key]);
      } else if (field.type === "number") {
        out[field.key] = Number(config && config[field.key]) || 0;
      } else {
        out[field.key] = String((config && config[field.key]) || "");
      }
    });
    return out;
  }

  function readNodeConfig(nodeEl, stepType) {
    var def = STEP_DEFS[stepType];
    if (!def) {
      return {};
    }
    var cfg = {};
    def.fields.forEach(function (field) {
      var input = nodeEl.querySelector('[data-field="' + field.key + '"]');
      if (!input) {
        return;
      }
      if (field.list) {
        cfg[field.key] = textToList(input.value);
      } else if (field.type === "number") {
        cfg[field.key] = Number(input.value) || 0;
      } else {
        cfg[field.key] = input.value.trim();
      }
    });
    return cfg;
  }

  function FlowBuilder(root) {
    this.root = root;
    this.form = root.closest("form");
    this.stepsInput = this.form.querySelector("#flow-steps-input");
    this.outputsInput = this.form.querySelector("#flow-outputs-input");
    this.stepsList = root.querySelector("#flow-steps-list");
    this.outputsList = root.querySelector("#flow-outputs-list");
    this.endLabel = root.querySelector("#flow-end-label");
    this.reviewBadge = root.querySelector("#flow-review-badge");
    this.palette = root.querySelector("#flow-palette");
    this.advancedToggle = root.querySelector("#flow-advanced-toggle");
    this.advancedPanel = root.querySelector("#flow-advanced-panel");
    this.stepsText = this.form.querySelector("#flow-steps-text");
    this.outputsText = this.form.querySelector("#flow-outputs-text");

    var init = parseInit(root);
    this.profiles = init.profiles || init.Profiles || [];
    this.steps = (init.steps || init.Steps || []).map(function (s) {
      return {
        id: s.id || uid("step"),
        stepType: s.stepType || s.StepType || "",
        enabled: s.enabled !== false && s.Enabled !== false,
        config: configToFields(
          s.stepType || s.StepType,
          tryParseJSON(s.configJSON || s.ConfigJSON || s.config_json)
        ),
      };
    });
    this.outputs = (init.outputs || init.Outputs || []).map(function (o) {
      return {
        id: o.id || uid("out"),
        profileId: o.profileID || o.profileId || o.ProfileID || "",
        suffix: o.suffix || o.Suffix || "",
        replaceExtension:
          o.replaceExtension === true ||
          o.ReplaceExtension === true ||
          o.replace_extension === true,
        enabled: o.enabled !== false && o.Enabled !== false,
      };
    });

    if (this.steps.length === 0) {
      this.steps.push({
        id: uid("step"),
        stepType: "filter.skip_if_codec",
        enabled: true,
        config: configToFields("filter.skip_if_codec", STEP_DEFS["filter.skip_if_codec"].defaults),
      });
    }
    if (this.outputs.length === 0) {
      this.outputs.push({
        id: uid("out"),
        profileId: this.profiles[0] ? this.profiles[0].id || this.profiles[0].ID : "hevc_gpu",
        suffix: "-hevc",
        replaceExtension: true,
        enabled: true,
      });
    }

    this.bind();
    this.render();
    this.syncHidden();
  }

  function tryParseJSON(raw) {
    if (!raw) {
      return null;
    }
    if (typeof raw === "object") {
      return raw;
    }
    try {
      return JSON.parse(raw);
    } catch (e) {
      return null;
    }
  }

  FlowBuilder.prototype.bind = function () {
    var self = this;

    this.palette.addEventListener("click", function (e) {
      var btn = e.target.closest("[data-add-step]");
      if (!btn) {
        return;
      }
      var stepType = btn.getAttribute("data-add-step");
      var def = STEP_DEFS[stepType];
      if (!def) {
        return;
      }
      self.steps.push({
        id: uid("step"),
        stepType: stepType,
        enabled: true,
        config: configToFields(stepType, def.defaults),
      });
      self.renderSteps();
      self.syncHidden();
    });

    rootAddOutputListener(this);

    this.form.addEventListener("submit", function () {
      self.collectFromDOM();
      self.syncHidden();
    });

    ["source_disposition", "hold_for_review"].forEach(function (name) {
      var el = self.form.querySelector('[name="' + name + '"]');
      if (!el) {
        return;
      }
      el.addEventListener("change", function () {
        self.updateEndNode();
      });
    });

    if (this.advancedToggle && this.advancedPanel) {
      this.advancedToggle.addEventListener("click", function () {
        self.advancedPanel.classList.toggle("hidden");
      });
    }
  };

  function rootAddOutputListener(self) {
    var addOut = self.root.querySelector("#flow-add-output");
    if (!addOut) {
      return;
    }
    addOut.addEventListener("click", function () {
      self.outputs.push({
        id: uid("out"),
        profileId: self.profiles[0] ? self.profiles[0].id || self.profiles[0].ID : "h264_fast",
        suffix: "-transcoded",
        replaceExtension: false,
        enabled: true,
      });
      self.renderOutputs();
      self.syncHidden();
    });
  }

  FlowBuilder.prototype.collectFromDOM = function () {
    var self = this;
    this.steps.forEach(function (step) {
      var node = self.stepsList.querySelector('[data-step-id="' + step.id + '"]');
      if (!node) {
        return;
      }
      var enabledEl = node.querySelector("[data-step-enabled]");
      step.enabled = enabledEl ? enabledEl.checked : step.enabled;
      step.config = readNodeConfig(node, step.stepType);
    });
    this.outputs.forEach(function (out) {
      var node = self.outputsList.querySelector('[data-output-id="' + out.id + '"]');
      if (!node) {
        return;
      }
      var profileEl = node.querySelector("[data-output-profile]");
      var suffixEl = node.querySelector("[data-output-suffix]");
      var replaceEl = node.querySelector("[data-output-replace]");
      var outEnabledEl = node.querySelector("[data-output-enabled]");
      out.profileId = profileEl ? profileEl.value : out.profileId;
      out.suffix = suffixEl ? suffixEl.value : "";
      out.replaceExtension = replaceEl ? replaceEl.checked : false;
      out.enabled = outEnabledEl ? outEnabledEl.checked : out.enabled;
    });
  };

  FlowBuilder.prototype.syncHidden = function () {
    var stepLines = this.steps
      .filter(function (s) {
        return s.enabled && s.stepType;
      })
      .map(function (s) {
        return s.stepType + "," + JSON.stringify(s.config || {});
      });
    var outLines = this.outputs
      .filter(function (o) {
        return o.enabled && o.profileId;
      })
      .map(function (o) {
        return (
          o.profileId +
          "," +
          (o.suffix || "") +
          "," +
          (o.replaceExtension ? "true" : "false")
        );
      });

    if (this.stepsInput) {
      this.stepsInput.value = stepLines.join("\n");
    }
    if (this.outputsInput) {
      this.outputsInput.value = outLines.join("\n");
    }
    if (this.stepsText) {
      this.stepsText.value = stepLines.join("\n");
    }
    if (this.outputsText) {
      this.outputsText.value = outLines.join("\n");
    }
  };

  FlowBuilder.prototype.render = function () {
    this.renderSteps();
    this.renderOutputs();
    this.updateEndNode();
  };

  FlowBuilder.prototype.updateEndNode = function () {
    var disp = this.form.querySelector('[name="source_disposition"]');
    var hold = this.form.querySelector('[name="hold_for_review"]');
    var label = disp ? disp.value : "keep";
    if (this.endLabel) {
      this.endLabel.textContent = "Source: " + label;
    }
    if (this.reviewBadge) {
      var show =
        hold &&
        (hold.checked || hold.value === "1") &&
        label !== "keep";
      this.reviewBadge.classList.toggle("hidden", !show);
    }
  };

  FlowBuilder.prototype.renderSteps = function () {
    var self = this;
    this.stepsList.innerHTML = "";
    this.steps.forEach(function (step, index) {
      self.stepsList.appendChild(self.renderStepNode(step, index));
    });
    this.bindDrag(this.stepsList, function (from, to) {
      var item = self.steps.splice(from, 1)[0];
      self.steps.splice(to, 0, item);
      self.renderSteps();
      self.syncHidden();
    });
  };

  FlowBuilder.prototype.renderOutputs = function () {
    var self = this;
    this.outputsList.innerHTML = "";
    this.outputs.forEach(function (out, index) {
      self.outputsList.appendChild(self.renderOutputNode(out, index));
    });
    this.bindDrag(this.outputsList, function (from, to) {
      var item = self.outputs.splice(from, 1)[0];
      self.outputs.splice(to, 0, item);
      self.renderOutputs();
      self.syncHidden();
    });
  };

  FlowBuilder.prototype.renderStepNode = function (step, index) {
    var def = STEP_DEFS[step.stepType] || { label: step.stepType, fields: [] };
    var wrap = document.createElement("div");
    wrap.className =
      "flow-node group rounded-lg border border-gray-700 bg-gray-950/80 p-3 cursor-grab active:cursor-grabbing";
    wrap.setAttribute("data-step-id", step.id);
    wrap.setAttribute("draggable", "true");
    wrap.setAttribute("data-testid", "flow-step-node");

    var header =
      '<div class="flex items-start justify-between gap-2 mb-2">' +
      '<div class="flex items-center gap-2 min-w-0">' +
      '<span class="text-xs text-gray-500 font-mono">' +
      String(index + 1) +
      "</span>" +
      '<span class="text-sm font-medium text-gray-200 truncate">' +
      escapeHtml(def.label) +
      "</span>" +
      '<span class="text-[10px] uppercase tracking-wide text-gray-500">' +
      escapeHtml((def.kind || "step") + "") +
      "</span>" +
      "</div>" +
      '<div class="flex items-center gap-2 shrink-0">' +
      '<label class="flex items-center gap-1 text-xs text-gray-400"><input type="checkbox" data-step-enabled' +
      (step.enabled ? " checked" : "") +
      ' class="rounded border-gray-600">On</label>' +
      '<button type="button" data-remove-step class="text-xs text-red-400 hover:text-red-300">Remove</button>' +
      "</div></div>";

    var fieldsHtml = def.fields
      .map(function (field) {
        var val = step.config[field.key];
        if (field.list) {
          val = listToText(val);
        }
        return (
          '<label class="block text-xs text-gray-400 mb-1">' +
          escapeHtml(field.label) +
          '<input data-field="' +
          escapeHtml(field.key) +
          '" value="' +
          escapeHtml(String(val == null ? "" : val)) +
          '" class="mt-1 w-full rounded border border-gray-700 bg-gray-900 px-2 py-1 text-sm text-gray-200">' +
          "</label>"
        );
      })
      .join("");

    wrap.innerHTML = header + '<div class="grid grid-cols-1 md:grid-cols-2 gap-2">' + fieldsHtml + "</div>";

    var self = this;
    wrap.querySelector("[data-remove-step]").addEventListener("click", function () {
      self.steps = self.steps.filter(function (s) {
        return s.id !== step.id;
      });
      self.renderSteps();
      self.syncHidden();
    });
    wrap.querySelectorAll("input").forEach(function (input) {
      input.addEventListener("change", function () {
        self.syncHidden();
      });
      input.addEventListener("input", function () {
        self.syncHidden();
      });
    });

    return wrap;
  };

  FlowBuilder.prototype.renderOutputNode = function (out, index) {
    var wrap = document.createElement("div");
    wrap.className =
      "flow-node rounded-lg border border-indigo-900/50 bg-indigo-950/20 p-3 cursor-grab active:cursor-grabbing";
    wrap.setAttribute("data-output-id", out.id);
    wrap.setAttribute("draggable", "true");
    wrap.setAttribute("data-testid", "flow-output-node");

    var profileOptions = this.profiles
      .map(function (p) {
        var id = p.id || p.ID;
        var name = p.name || p.Name || id;
        var selected = id === out.profileId ? " selected" : "";
        return (
          '<option value="' +
          escapeHtml(id) +
          '"' +
          selected +
          ">" +
          escapeHtml(name) +
          " (" +
          escapeHtml(id) +
          ")</option>"
        );
      })
      .join("");

    wrap.innerHTML =
      '<div class="flex items-start justify-between gap-2 mb-2">' +
      '<div class="text-sm font-medium text-indigo-200">Output ' +
      String(index + 1) +
      "</div>" +
      '<div class="flex items-center gap-2">' +
      '<label class="flex items-center gap-1 text-xs text-gray-400"><input type="checkbox" data-output-enabled' +
      (out.enabled ? " checked" : "") +
      ' class="rounded border-gray-600">On</label>' +
      '<button type="button" data-remove-output class="text-xs text-red-400 hover:text-red-300">Remove</button>' +
      "</div></div>" +
      '<div class="grid grid-cols-1 md:grid-cols-3 gap-2">' +
      '<label class="block text-xs text-gray-400">Profile<select data-output-profile class="mt-1 w-full rounded border border-gray-700 bg-gray-900 px-2 py-1 text-sm">' +
      profileOptions +
      "</select></label>" +
      '<label class="block text-xs text-gray-400">Suffix<input data-output-suffix value="' +
      escapeHtml(out.suffix || "") +
      '" placeholder="-hevc" class="mt-1 w-full rounded border border-gray-700 bg-gray-900 px-2 py-1 text-sm font-mono"></label>' +
      '<label class="flex items-end gap-2 text-xs text-gray-400 pb-1"><input type="checkbox" data-output-replace' +
      (out.replaceExtension ? " checked" : "") +
      ' class="rounded border-gray-600">Replace extension</label>' +
      "</div>";

    var self = this;
    wrap.querySelector("[data-remove-output]").addEventListener("click", function () {
      self.outputs = self.outputs.filter(function (o) {
        return o.id !== out.id;
      });
      self.renderOutputs();
      self.syncHidden();
    });
    wrap.querySelectorAll("input,select").forEach(function (input) {
      input.addEventListener("change", function () {
        self.syncHidden();
      });
      input.addEventListener("input", function () {
        self.syncHidden();
      });
    });

    return wrap;
  };

  FlowBuilder.prototype.bindDrag = function (listEl, onReorder) {
    var dragIndex = null;
    listEl.querySelectorAll(".flow-node").forEach(function (node) {
      node.addEventListener("dragstart", function (e) {
        dragIndex = Array.prototype.indexOf.call(listEl.children, node);
        node.classList.add("opacity-50");
        e.dataTransfer.effectAllowed = "move";
      });
      node.addEventListener("dragend", function () {
        node.classList.remove("opacity-50");
        dragIndex = null;
      });
      node.addEventListener("dragover", function (e) {
        e.preventDefault();
        e.dataTransfer.dropEffect = "move";
      });
      node.addEventListener("drop", function (e) {
        e.preventDefault();
        if (dragIndex == null) {
          return;
        }
        var dropIndex = Array.prototype.indexOf.call(listEl.children, node);
        if (dropIndex === dragIndex || dropIndex < 0) {
          return;
        }
        onReorder(dragIndex, dropIndex);
      });
    });
  };

  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function init() {
    var root = document.getElementById("transcode-flow-builder");
    if (!root) {
      return;
    }
    new FlowBuilder(root);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();
