$(document).ready(function () {
    $('[data-open-view]').click(function () {
        const view = $(this).data('open-view');
        $(`#nav-${view}`).trigger('click');
    });
});
