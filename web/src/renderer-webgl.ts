import type { Framebuffer } from './framebuffer';
import type { IRenderer } from './renderer-interface';

const VERTEX_SHADER = `#version 300 es
in vec2 a_position;
in vec2 a_texCoord;
out vec2 v_texCoord;
void main() {
  gl_Position = vec4(a_position, 0.0, 1.0);
  v_texCoord = a_texCoord;
}`;

const FRAGMENT_SHADER = `#version 300 es
precision mediump float;
in vec2 v_texCoord;
out vec4 fragColor;
uniform sampler2D u_texture;
void main() {
  fragColor = texture(u_texture, v_texCoord);
}`;

/**
 * WebGL 2 renderer that uploads the framebuffer as a GPU texture
 * and renders it with a fullscreen quad. Dirty rectangles are uploaded
 * via texSubImage2D for efficient partial updates.
 */
export class WebGLRenderer implements IRenderer {
  private canvas: HTMLCanvasElement | null = null;
  private gl: WebGL2RenderingContext | null = null;
  private texture: WebGLTexture | null = null;
  private program: WebGLProgram | null = null;
  private vao: WebGLVertexArrayObject | null = null;
  private texWidth = 0;
  private texHeight = 0;
  private scaleToFit: boolean;

  constructor(scaleToFit: boolean = false) {
    this.scaleToFit = scaleToFit;
  }

  attach(canvas: HTMLCanvasElement): void {
    this.canvas = canvas;
    const gl = canvas.getContext('webgl2', {
      alpha: false,
      antialias: false,
      desynchronized: true,
      preserveDrawingBuffer: false,
    });
    if (!gl) {
      throw new Error('WebGL 2 not available');
    }
    this.gl = gl;
    this.initGL(gl);
  }

  detach(): void {
    if (this.gl) {
      if (this.texture) this.gl.deleteTexture(this.texture);
      if (this.program) this.gl.deleteProgram(this.program);
      if (this.vao) this.gl.deleteVertexArray(this.vao);
    }
    this.gl = null;
    this.canvas = null;
    this.texture = null;
    this.program = null;
    this.vao = null;
    this.texWidth = 0;
    this.texHeight = 0;
  }

  get attached(): boolean {
    return this.canvas !== null;
  }

  setScaleToFit(scale: boolean): void {
    this.scaleToFit = scale;
  }

  private initGL(gl: WebGL2RenderingContext): void {
    // Compile shaders
    const vs = this.compileShader(gl, gl.VERTEX_SHADER, VERTEX_SHADER);
    const fs = this.compileShader(gl, gl.FRAGMENT_SHADER, FRAGMENT_SHADER);
    const program = gl.createProgram()!;
    gl.attachShader(program, vs);
    gl.attachShader(program, fs);
    gl.linkProgram(program);
    if (!gl.getProgramParameter(program, gl.LINK_STATUS)) {
      throw new Error('Shader link failed: ' + gl.getProgramInfoLog(program));
    }
    gl.deleteShader(vs);
    gl.deleteShader(fs);
    this.program = program;

    // Fullscreen quad (two triangles, clip space coords + tex coords)
    // Positions: covers [-1,1] clip space
    // TexCoords: covers [0,1] with Y flipped (top-left origin)
    const vertices = new Float32Array([
      // pos        texCoord
      -1, -1,      0, 1,
       1, -1,      1, 1,
      -1,  1,      0, 0,
       1,  1,      1, 0,
    ]);

    const vao = gl.createVertexArray()!;
    gl.bindVertexArray(vao);

    const vbo = gl.createBuffer()!;
    gl.bindBuffer(gl.ARRAY_BUFFER, vbo);
    gl.bufferData(gl.ARRAY_BUFFER, vertices, gl.STATIC_DRAW);

    const posLoc = gl.getAttribLocation(program, 'a_position');
    gl.enableVertexAttribArray(posLoc);
    gl.vertexAttribPointer(posLoc, 2, gl.FLOAT, false, 16, 0);

    const texLoc = gl.getAttribLocation(program, 'a_texCoord');
    gl.enableVertexAttribArray(texLoc);
    gl.vertexAttribPointer(texLoc, 2, gl.FLOAT, false, 16, 8);

    gl.bindVertexArray(null);
    this.vao = vao;

    // Create texture
    const texture = gl.createTexture()!;
    gl.bindTexture(gl.TEXTURE_2D, texture);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
    gl.bindTexture(gl.TEXTURE_2D, null);
    this.texture = texture;
  }

  private compileShader(gl: WebGL2RenderingContext, type: number, source: string): WebGLShader {
    const shader = gl.createShader(type)!;
    gl.shaderSource(shader, source);
    gl.compileShader(shader);
    if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) {
      const info = gl.getShaderInfoLog(shader);
      gl.deleteShader(shader);
      throw new Error('Shader compile failed: ' + info);
    }
    return shader;
  }

  updateCanvasSize(fb: Framebuffer): void {
    if (!this.canvas || !this.gl) return;

    // Always set canvas internal resolution to match framebuffer
    this.canvas.width = fb.width;
    this.canvas.height = fb.height;
    this.gl.viewport(0, 0, fb.width, fb.height);

    // Reallocate texture if size changed
    if (fb.width !== this.texWidth || fb.height !== this.texHeight) {
      this.texWidth = fb.width;
      this.texHeight = fb.height;

      this.gl.bindTexture(this.gl.TEXTURE_2D, this.texture);
      this.gl.texImage2D(
        this.gl.TEXTURE_2D, 0, this.gl.RGBA,
        fb.width, fb.height, 0,
        this.gl.RGBA, this.gl.UNSIGNED_BYTE,
        null, // allocate without data
      );
      this.gl.bindTexture(this.gl.TEXTURE_2D, null);
    }
  }

  /**
   * Render dirty regions from the framebuffer to the canvas via WebGL.
   * Uses texSubImage2D for efficient partial texture updates.
   */
  render(fb: Framebuffer): void {
    const gl = this.gl;
    if (!gl || !this.texture || !this.program || !this.vao) return;

    const dirtyRects = fb.dirtyRects;
    if (dirtyRects.length === 0) return;

    gl.bindTexture(gl.TEXTURE_2D, this.texture);

    // Upload dirty regions to the texture
    // We need to extract the dirty sub-rectangles from the full ImageData
    const fbData = fb.imageData.data;
    const fbWidth = fb.width;

    for (const rect of dirtyRects) {
      // Extract the sub-rectangle pixels
      const subPixels = new Uint8Array(rect.w * rect.h * 4);
      for (let row = 0; row < rect.h; row++) {
        const srcOff = ((rect.y + row) * fbWidth + rect.x) * 4;
        const dstOff = row * rect.w * 4;
        subPixels.set(fbData.subarray(srcOff, srcOff + rect.w * 4), dstOff);
      }

      gl.texSubImage2D(
        gl.TEXTURE_2D, 0,
        rect.x, rect.y,
        rect.w, rect.h,
        gl.RGBA, gl.UNSIGNED_BYTE,
        subPixels,
      );
    }

    // Draw the fullscreen quad
    gl.useProgram(this.program);
    gl.bindVertexArray(this.vao);
    gl.drawArrays(gl.TRIANGLE_STRIP, 0, 4);
    gl.bindVertexArray(null);

    fb.clearDirty();
  }

  setCursor(imageData: Uint8Array, width: number, height: number, hotX: number, hotY: number): void {
    if (!this.canvas) return;

    const cursorCanvas = document.createElement('canvas');
    cursorCanvas.width = width;
    cursorCanvas.height = height;
    const ctx = cursorCanvas.getContext('2d')!;
    const imgData = ctx.createImageData(width, height);
    imgData.data.set(imageData);
    ctx.putImageData(imgData, 0, 0);

    const dataUrl = cursorCanvas.toDataURL('image/png');
    this.canvas.style.cursor = `url(${dataUrl}) ${hotX} ${hotY}, auto`;
  }

  translateCoordinates(
    event: MouseEvent,
    fbWidth: number,
    fbHeight: number,
  ): { x: number; y: number } {
    if (!this.canvas) return { x: 0, y: 0 };

    const rect = this.canvas.getBoundingClientRect();
    const canvasX = event.clientX - rect.left;
    const canvasY = event.clientY - rect.top;

    if (this.scaleToFit) {
      const scaleX = fbWidth / rect.width;
      const scaleY = fbHeight / rect.height;
      return {
        x: Math.floor(canvasX * scaleX),
        y: Math.floor(canvasY * scaleY),
      };
    }

    return {
      x: Math.floor(canvasX),
      y: Math.floor(canvasY),
    };
  }
}
