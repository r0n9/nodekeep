package controller

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/r0n9/nodekeep/model"
	"github.com/r0n9/nodekeep/pkg/mygin"
	"github.com/r0n9/nodekeep/service/dao"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
	"gorm.io/gorm"
)

type guestPage struct {
	r *gin.Engine
}

func (gp *guestPage) serve() {
	gr := gp.r.Group("")
	gr.Use(mygin.Authorize(mygin.AuthorizeOption{
		Guest:    true,
		IsPage:   true,
		Msg:      "您已登录",
		Btn:      "返回首页",
		Redirect: "/",
	}))

	gr.GET("/login", gp.login)
	gr.POST("/login", gp.localLogin)

	var endPoint oauth2.Endpoint

	if dao.Conf.Oauth2.Type == model.ConfigTypeGitee {
		endPoint = oauth2.Endpoint{
			AuthURL:  "https://gitee.com/oauth/authorize",
			TokenURL: "https://gitee.com/oauth/token",
		}
	} else {
		endPoint = github.Endpoint
	}

	oauth := &oauth2controller{
		oauth2Config: &oauth2.Config{
			ClientID:     dao.Conf.Oauth2.ClientID,
			ClientSecret: dao.Conf.Oauth2.ClientSecret,
			Scopes:       []string{},
			Endpoint:     endPoint,
		},
		r: gr,
	}
	oauth.serve()
}

func (gp *guestPage) login(c *gin.Context) {
	c.HTML(http.StatusOK, "dashboard/login", mygin.CommonEnvironment(c, gin.H{
		"Title": "登录",
	}))
}

type localLoginForm struct {
	Username string
	Password string
}

func (gp *guestPage) localLogin(c *gin.Context) {
	var lf localLoginForm
	if err := c.ShouldBind(&lf); err != nil {
		gp.showLoginFailed(c, fmt.Sprintf("请求错误：%s", err))
		return
	}

	username := strings.TrimSpace(lf.Username)
	password := lf.Password
	conf := dao.Conf.Auth.Local
	if !conf.Enabled || conf.Username == "" || conf.Password == "" {
		gp.showLoginFailed(c, "本地账号登录未启用")
		return
	}
	if subtle.ConstantTimeCompare([]byte(username), []byte(conf.Username)) != 1 ||
		subtle.ConstantTimeCompare([]byte(password), []byte(conf.Password)) != 1 {
		gp.showLoginFailed(c, "账号或密码错误")
		return
	}

	var user model.User
	if err := dao.DB.Where("login = ?", username).First(&user).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			gp.showLoginFailed(c, fmt.Sprintf("读取用户失败：%s", err))
			return
		}
		user = model.User{
			Login:      username,
			Name:       username,
			SuperAdmin: true,
		}
	}
	user.IssueNewToken()
	if err := dao.DB.Save(&user).Error; err != nil {
		gp.showLoginFailed(c, fmt.Sprintf("写入用户失败：%s", err))
		return
	}
	c.SetCookie(dao.Conf.Site.CookieName, user.Token, 60*60*24, "", "", false, false)
	c.Redirect(http.StatusFound, "/")
}

func (gp *guestPage) showLoginFailed(c *gin.Context, msg string) {
	mygin.ShowErrorPage(c, mygin.ErrInfo{
		Code:  http.StatusBadRequest,
		Title: "登录失败",
		Msg:   fmt.Sprintf("错误信息：%s", msg),
		Link:  "/login",
		Btn:   "返回登录",
	}, true)
}
